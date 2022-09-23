// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/assert"
)

func Test_detectProviders(t *testing.T) {
	tempDir := t.TempDir()
	ctx := context.Background()

	t.Run("no azd context within context", func(t *testing.T) {
		scmProvider, ciProvider, err := DetectProviders(ctx, &environment.Environment{}, "")
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, "cannot find AzdContext on go context")
	})

	azdContext := &azdcontext.AzdContext{}
	azdContext.SetProjectDirectory(tempDir)
	ctx = azdcontext.WithAzdContext(ctx, azdContext)

	t.Run("no folders error", func(t *testing.T) {
		scmProvider, ciProvider, err := DetectProviders(ctx, &nullConsole{}, nil)
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, "no CI/CD provider configuration found. Expecting either .github and/or .azdo folder in the project root directory")
	})
	t.Run("github folder only", func(t *testing.T) {
		ghFolder := path.Join(tempDir, ".github")
		err := os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(ctx, &nullConsole{}, nil)
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

		scmProvider, ciProvider, err := DetectProviders(ctx, &nullConsole{}, nil)
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

		scmProvider, ciProvider, err := DetectProviders(ctx, &selectDefaultConsole{}, nil)
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
		}, nil)
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
		}, nil)
		assert.IsType(t, &GitHubScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)

		// Remove folder - reset state
		os.Remove(azdoFolder)
		os.Remove(ghFolder)
	})
}
