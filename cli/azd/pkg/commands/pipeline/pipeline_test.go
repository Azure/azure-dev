// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/assert"
)

func Test_detectProviders(t *testing.T) {
	tempDir := t.TempDir()
	ctx := context.Background()

	t.Run("no azd context within context", func(t *testing.T) {
		scmProvider, ciProvider, err := DetectProviders(ctx, &nullConsole{})
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, "cannot find AzdContext on go context")
	})

	azdContext := &azdcontext.AzdContext{}
	azdContext.SetProjectDirectory(tempDir)
	ctx = azdcontext.WithAzdContext(ctx, azdContext)

	t.Run("no folders error", func(t *testing.T) {
		scmProvider, ciProvider, err := DetectProviders(ctx, &nullConsole{})
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, "no CI/CD provider found in template root folders.")
	})
	t.Run("github folder only", func(t *testing.T) {
		ghFolder := path.Join(tempDir, ".github")
		err := os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(ctx, &nullConsole{})
		assert.IsType(t, &GitHubScmProvider{}, scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, ciProvider)
		assert.NoError(t, err)
		// Remove folder - reset state
		os.Remove(ghFolder)
	})
	t.Run("azdo folder only", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, ".azdo")
		err := os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(ctx, &nullConsole{})
		assert.IsType(t, &AzdoHubScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)
		// Remove folder - reset state
		os.Remove(azdoFolder)
	})
	t.Run("multi provider select defaults", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, ".azdo")
		ghFolder := path.Join(tempDir, ".github")
		err := os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		err = os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(ctx, &selectDefaultConsole{})
		assert.IsType(t, &GitHubScmProvider{}, scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, ciProvider)
		assert.NoError(t, err)

		// Remove folder - reset state
		os.Remove(azdoFolder)
		os.Remove(ghFolder)
	})
	t.Run("multi provider select azdo", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, ".azdo")
		ghFolder := path.Join(tempDir, ".github")
		err := os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		err = os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(ctx, &circularConsole{
			selectReturnValues: []int{1},
		})
		assert.IsType(t, &AzdoHubScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)

		// Remove folder - reset state
		os.Remove(azdoFolder)
		os.Remove(ghFolder)
	})
	t.Run("multi provider select git and azdo", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, ".azdo")
		ghFolder := path.Join(tempDir, ".github")
		err := os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		err = os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(ctx, &circularConsole{
			selectReturnValues: []int{0, 1},
		})
		assert.IsType(t, &GitHubScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)

		// Remove folder - reset state
		os.Remove(azdoFolder)
		os.Remove(ghFolder)
	})
}

// ------------- Test implementations -------------------
// Test consoles to control input and define deterministic tests

// For tests where the console won't matter at all
type nullConsole struct {
}

func (console *nullConsole) Message(ctx context.Context, message string) {}
func (console *nullConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	return "", nil
}
func (console *nullConsole) Select(ctx context.Context, options input.ConsoleOptions) (int, error) {
	return 0, nil
}
func (console *nullConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	return false, nil
}
func (console *nullConsole) SetWriter(writer io.Writer) {}

// For tests where console.prompt returns defaults only
type selectDefaultConsole struct {
}

func (console *selectDefaultConsole) Message(ctx context.Context, message string) {}
func (console *selectDefaultConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	return options.DefaultValue.(string), nil
}
func (console *selectDefaultConsole) Select(ctx context.Context, options input.ConsoleOptions) (int, error) {
	for index, value := range options.Options {
		if value == options.DefaultValue {
			return index, nil
		}
	}
	return 0, nil
}
func (console *selectDefaultConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	return false, nil
}
func (console *selectDefaultConsole) SetWriter(writer io.Writer) {}

// For tests where console.prompt returns values provided in its internal []string
type circularConsole struct {
	selectReturnValues []int
	index              int
}

func (console *circularConsole) Message(ctx context.Context, message string) {}
func (console *circularConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	return "", nil
}

func (console *circularConsole) Select(ctx context.Context, options input.ConsoleOptions) (int, error) {
	// If no values where provided, return error
	arraySize := len(console.selectReturnValues)
	if arraySize == 0 {
		return 0, errors.New("no values to return")
	}

	// Reset index when it reaches size (back to first value)
	if console.index == arraySize {
		console.index = 0
	}

	returnValue := console.selectReturnValues[console.index]
	console.index += 1
	return returnValue, nil
}
func (console *circularConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	return false, nil
}
func (console *circularConsole) SetWriter(writer io.Writer) {}
