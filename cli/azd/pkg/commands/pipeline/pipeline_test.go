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
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
)

func Test_detectProviders(t *testing.T) {
	tempDir := t.TempDir()
	ctx := context.Background()

	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)

	mockContext := mocks.NewMockContext(ctx)

	t.Run("no folders error", func(t *testing.T) {
		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			environment.Ephemeral(),
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(
			t,
			err,
			"no CI/CD provider configuration found. Expecting either github and/or azdo folder in the project root directory.",
		)
	})

	t.Run("can't load project settings", func(t *testing.T) {
		ghFolder := path.Join(tempDir, githubFolder)
		err := os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			environment.Ephemeral(),
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.ErrorContains(
			t, err, "finding pipeline provider: reading project file:")
		os.Remove(ghFolder)
	})

	projectFileName := path.Join(tempDir, "azure.yaml")
	projectFile, err := os.Create(projectFileName)
	assert.NoError(t, err)
	defer projectFile.Close()

	t.Run("from persisted data azdo error", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, githubFolder)
		err := os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = azdoLabel
		env := &environment.Environment{
			Values: envValues,
		}

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, ".azdo folder is missing. Can't use selected provider.")

		os.Remove(azdoFolder)
	})
	t.Run("from persisted data azdo", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, azdoFolder)
		err := os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = azdoLabel
		env := &environment.Environment{
			Values: envValues,
		}

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &AzdoScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)

		os.Remove(azdoFolder)
	})
	t.Run("from persisted data github error", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, azdoFolder)
		err := os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = gitHubLabel
		env := &environment.Environment{
			Values: envValues,
		}
		scmProvider, ciProvider, err := DetectProviders(ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, ".github folder is missing. Can't use selected provider.")

		os.Remove(azdoFolder)
	})
	t.Run("from persisted data github", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, githubFolder)
		err := os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = gitHubLabel
		env := &environment.Environment{
			Values: envValues,
		}

		scmProvider, ciProvider, err := DetectProviders(ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &GitHubScmProvider{}, scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, ciProvider)
		assert.NoError(t, err)

		os.Remove(azdoFolder)
	})

	t.Run("unknown override value from arg", func(t *testing.T) {
		ghFolder := path.Join(tempDir, githubFolder)
		err := os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			&environment.Environment{Values: map[string]string{}},
			"other",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, "other is not a known pipeline provider.", nil, nil)

		// Remove folder - reset state
		os.Remove(ghFolder)
	})
	t.Run("unknown override value from env", func(t *testing.T) {
		ghFolder := path.Join(tempDir, githubFolder)
		err := os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = "other"
		env := &environment.Environment{
			Values: envValues,
		}

		scmProvider, ciProvider, err := DetectProviders(ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, "other is not a known pipeline provider.")

		// Remove folder - reset state
		os.Remove(ghFolder)
	})
	t.Run("unknown override value from yaml", func(t *testing.T) {
		ghFolder := path.Join(tempDir, githubFolder)
		err := os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		_, err = projectFile.WriteString("pipeline:\n\r  provider: other")
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			environment.Ephemeral(),
			"", mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, "other is not a known pipeline provider.")

		// Remove folder - reset state
		os.Remove(ghFolder)
		projectFile.Close()
		os.Remove(projectFileName)
		projectFile, err = os.Create(projectFileName)
		assert.NoError(t, err)
	})
	t.Run("override persisted value with yaml", func(t *testing.T) {
		ghFolder := path.Join(tempDir, githubFolder)
		err := os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		_, err = projectFile.WriteString("pipeline:\n\r  provider: fromYaml")
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = "persisted"
		env := &environment.Environment{
			Values: envValues,
		}

		scmProvider, ciProvider, err := DetectProviders(ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, "fromYaml is not a known pipeline provider.")

		// Remove folder - reset state
		os.Remove(ghFolder)
		projectFile.Close()
		os.Remove(projectFileName)
		projectFile, err = os.Create(projectFileName)
		assert.NoError(t, err)
	})
	t.Run("override persisted and yaml with arg", func(t *testing.T) {
		ghFolder := path.Join(tempDir, githubFolder)
		err := os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		_, err = projectFile.WriteString("pipeline:\n\r  provider: fromYaml")
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = "persisted"
		env := &environment.Environment{
			Values: envValues,
		}

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			env,
			"arg",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.Nil(t, scmProvider)
		assert.Nil(t, ciProvider)
		assert.EqualError(t, err, "arg is not a known pipeline provider.")

		// Remove folder - reset state
		os.Remove(ghFolder)
		projectFile.Close()
		os.Remove(projectFileName)
		projectFile, err = os.Create(projectFileName)
		assert.NoError(t, err)
	})
	t.Run("github folder only", func(t *testing.T) {
		ghFolder := path.Join(tempDir, githubFolder)
		err := os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			&environment.Environment{Values: map[string]string{}},
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &GitHubScmProvider{}, scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, ciProvider)
		assert.NoError(t, err)

		os.Remove(ghFolder)
	})
	t.Run("azdo folder only", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, azdoFolder)
		err := os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			&environment.Environment{Values: map[string]string{}},
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &AzdoScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)

		os.Remove(azdoFolder)
	})
	t.Run("both folders and not arguments", func(t *testing.T) {
		ghFolder := path.Join(tempDir, githubFolder)
		err := os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		azdoFolder := path.Join(tempDir, azdoFolder)
		err = os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			&environment.Environment{Values: map[string]string{}},
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &GitHubScmProvider{}, scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, ciProvider)
		assert.NoError(t, err)

		os.Remove(ghFolder)
		os.Remove(azdoFolder)
	})
	t.Run("persist selection on environment", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, azdoFolder)
		err = os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		env := &environment.Environment{Values: map[string]string{}}

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			env,
			azdoLabel,
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &AzdoScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)

		envValue, found := env.Values[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, azdoLabel, envValue)

		// Calling function again with same env and without override arg should use the persisted
		scmProvider, ciProvider, err = DetectProviders(ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &AzdoScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)

		os.Remove(azdoFolder)
	})
	t.Run("persist selection on environment and override with yaml", func(t *testing.T) {
		azdoFolder := path.Join(tempDir, azdoFolder)
		err = os.Mkdir(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		ghFolder := path.Join(tempDir, githubFolder)
		err = os.Mkdir(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		env := &environment.Environment{Values: map[string]string{}}

		scmProvider, ciProvider, err := DetectProviders(
			ctx,
			azdContext,
			env,
			azdoLabel,
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &AzdoScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)

		// Calling function again with same env and without override arg should use the persisted
		scmProvider, ciProvider, err = DetectProviders(ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &AzdoScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)

		// Write yaml to override
		_, err = projectFile.WriteString("pipeline:\n\r  provider: github")
		assert.NoError(t, err)

		// Calling function again with same env and without override arg should detect yaml change and override
		// persisted
		scmProvider, ciProvider, err = DetectProviders(ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &GitHubScmProvider{}, scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, ciProvider)
		assert.NoError(t, err)

		// the persisted choice should be updated based on the value set on yaml
		envValue, found := env.Values[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, gitHubLabel, envValue)

		// Call again to check persisted(github) after one change (and yaml is still present)
		scmProvider, ciProvider, err = DetectProviders(ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &GitHubScmProvider{}, scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, ciProvider)
		assert.NoError(t, err)

		// Check argument override having yaml(github) config and persisted config(github)
		scmProvider, ciProvider, err = DetectProviders(
			ctx,
			azdContext,
			env,
			azdoLabel,
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &AzdoScmProvider{}, scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, ciProvider)
		assert.NoError(t, err)

		// the persisted selection is now azdo(env) but yaml is github
		envValue, found = env.Values[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, azdoLabel, envValue)

		// persisted = azdo (per last run) and yaml = github, should return github
		// as yaml overrides a persisted run
		scmProvider, ciProvider, err = DetectProviders(ctx,
			azdContext,
			env,
			"",
			mockContext.Console,
			mockContext.Credentials,
			mockContext.CommandRunner,
		)
		assert.IsType(t, &GitHubScmProvider{}, scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, ciProvider)
		assert.NoError(t, err)

		// reset state
		projectFile.Close()
		os.Remove(projectFileName)

		os.Remove(azdoFolder)
		os.Remove(ghFolder)
	})

}
