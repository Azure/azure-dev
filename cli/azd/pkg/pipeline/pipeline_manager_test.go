// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func Test_PipelineManager_Initialize(t *testing.T) {
	tempDir := t.TempDir()
	ctx := context.Background()

	azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
	mockContext := mocks.NewMockContext(ctx)
	setupGithubCliMocks(mockContext)

	t.Run("no folders error", func(t *testing.T) {
		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.Nil(t, manager)
		assert.EqualError(
			t,
			err,
			"no CI/CD provider configuration found. Expecting either github and/or azdo folder in the project root directory.",
		)
	})

	t.Run("can't load project settings", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, githubFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.Nil(t, manager)
		assert.ErrorContains(
			t, err, "Loading project configuration: reading project file:")
		os.Remove(ghFolder)
	})

	projectFileName := filepath.Join(tempDir, "azure.yaml")
	projectFile, err := os.Create(projectFileName)
	assert.NoError(t, err)
	_, err = projectFile.WriteString("name: test\n")
	assert.NoError(t, err)
	defer projectFile.Close()

	t.Run("from persisted data azdo error", func(t *testing.T) {
		azdoFolderTest := filepath.Join(tempDir, githubFolder)
		err := os.MkdirAll(azdoFolderTest, osutil.PermissionDirectory)
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = azdoLabel
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, fmt.Sprintf("%s folder is missing. Can't use selected provider", azdoFolder))

		os.Remove(azdoFolderTest)
	})
	t.Run("from persisted data azdo yml error", func(t *testing.T) {
		azdoFolderTest := filepath.Join(tempDir, azdoFolder)
		err := os.MkdirAll(azdoFolderTest, osutil.PermissionDirectory)
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = azdoLabel
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, fmt.Sprintf("%s file is missing in %s folder. Can't use selected provider",
			azdoYml, azdoFolder))

		os.Remove(azdoFolderTest)
	})
	t.Run("from persisted data azdo", func(t *testing.T) {
		azdoFolder := filepath.Join(tempDir, azdoFolder)
		err := os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		infraFolder := filepath.Join(tempDir, "infra")
		err = os.MkdirAll(infraFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		file, err := os.Create(filepath.Join(infraFolder, "main.foo"))
		file.Close()
		assert.NoError(t, err)

		file, err = os.Create(filepath.Join(tempDir, azdoYml))
		file.Close()
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = azdoLabel
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.NoError(t, err)
		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)

		os.Remove(azdoFolder)
	})
	t.Run("from persisted data github error", func(t *testing.T) {
		azdoFolder := filepath.Join(tempDir, azdoFolder)
		err := os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = gitHubLabel
		env := environment.NewWithValues("test-env", envValues)
		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, fmt.Sprintf("%s folder is missing. Can't use selected provider",
			githubFolder))

		os.Remove(azdoFolder)
	})
	t.Run("from persisted data github", func(t *testing.T) {
		azdoFolder := filepath.Join(tempDir, githubFolder)
		err := os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = gitHubLabel
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		os.Remove(azdoFolder)
	})
	t.Run("unknown override value from arg", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, githubFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		args := &PipelineManagerArgs{
			PipelineProvider: "other",
		}
		manager, err := createPipelineManager(t, mockContext, azdContext, nil, args)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "other is not a known pipeline provider")

		// Remove folder - reset state
		os.Remove(ghFolder)
	})
	t.Run("unknown override value from env", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, githubFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = "other"
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "other is not a known pipeline provider")

		// Remove folder - reset state
		os.Remove(ghFolder)
	})
	t.Run("unknown override value from yaml", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, githubFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		_, err = projectFile.WriteString("pipeline:\n\r  provider: other")
		assert.NoError(t, err)

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "other is not a known pipeline provider")

		// Remove folder - reset state
		os.Remove(ghFolder)
		projectFile.Close()
		os.Remove(projectFileName)
		projectFile, err = os.Create(projectFileName)
		assert.NoError(t, err)
		_, err = projectFile.WriteString("name: test\n")
		assert.NoError(t, err)
	})
	t.Run("override persisted value with yaml", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, githubFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		_, err = projectFile.WriteString("pipeline:\n\r  provider: fromYaml")
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = "persisted"
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "fromYaml is not a known pipeline provider")

		// Remove folder - reset state
		os.Remove(ghFolder)
		projectFile.Close()
		os.Remove(projectFileName)
		projectFile, err = os.Create(projectFileName)
		assert.NoError(t, err)
		_, err = projectFile.WriteString("name: test\n")
		assert.NoError(t, err)
	})
	t.Run("override persisted and yaml with arg", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, githubFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		_, err = projectFile.WriteString("pipeline:\n\r  provider: fromYaml")
		assert.NoError(t, err)

		envValues := map[string]string{}
		envValues[envPersistedKey] = "persisted"
		env := environment.NewWithValues("test-env", envValues)
		args := &PipelineManagerArgs{
			PipelineProvider: "arg",
		}

		manager, err := createPipelineManager(t, mockContext, azdContext, env, args)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "arg is not a known pipeline provider")

		// Remove folder - reset state
		os.Remove(ghFolder)
		projectFile.Close()
		os.Remove(projectFileName)
		projectFile, err = os.Create(projectFileName)
		assert.NoError(t, err)
		_, err = projectFile.WriteString("name: test\n")
		assert.NoError(t, err)
	})
	t.Run("github folder only", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, githubFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		os.Remove(ghFolder)
	})
	t.Run("azdo folder only", func(t *testing.T) {
		azdoFolder := filepath.Join(tempDir, azdoFolder)
		err := os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		os.Remove(azdoFolder)
	})
	t.Run("both folders and not arguments", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, githubFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		azdoFolder := filepath.Join(tempDir, azdoFolder)
		err = os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		os.Remove(ghFolder)
		os.Remove(azdoFolder)
	})
	t.Run("persist selection on environment", func(t *testing.T) {
		azdoFolder := filepath.Join(tempDir, azdoFolder)
		err = os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		env := environment.New("test")
		args := &PipelineManagerArgs{
			PipelineProvider: azdoLabel,
		}

		manager, err := createPipelineManager(t, mockContext, azdContext, env, args)
		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		envValue, found := env.Dotenv()[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, azdoLabel, envValue)

		// Calling function again with same env and without override arg should use the persisted
		err = manager.initialize(*mockContext.Context, "")
		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		os.Remove(azdoFolder)
	})
	t.Run("persist selection on environment and override with yaml", func(t *testing.T) {
		azdoFolder := filepath.Join(tempDir, azdoFolder)
		err = os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		ghFolder := filepath.Join(tempDir, githubFolder)
		err = os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		env := environment.New("test")
		args := &PipelineManagerArgs{
			PipelineProvider: azdoLabel,
		}
		manager, err := createPipelineManager(t, mockContext, azdContext, env, args)
		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		// Calling function again with same env and without override arg should use the persisted
		err = manager.initialize(*mockContext.Context, "")
		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		// Write yaml to override
		_, err = projectFile.WriteString("pipeline:\n\r  provider: github")
		assert.NoError(t, err)

		// Calling function again with same env and without override arg should detect yaml change and override persisted
		err = manager.initialize(*mockContext.Context, "")
		assert.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		// the persisted choice should be updated based on the value set on yaml
		envValue, found := env.Dotenv()[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, gitHubLabel, envValue)

		// Call again to check persisted(github) after one change (and yaml is still present)
		err = manager.initialize(*mockContext.Context, "")
		assert.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		// Check argument override having yaml(github) config and persisted config(github)
		err = manager.initialize(*mockContext.Context, azdoLabel)
		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		// the persisted selection is now azdo(env) but yaml is github
		envValue, found = env.Dotenv()[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, azdoLabel, envValue)

		// persisted = azdo (per last run) and yaml = github, should return github
		// as yaml overrides a persisted run
		err = manager.initialize(*mockContext.Context, "")
		assert.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, manager.ciProvider)
		assert.NoError(t, err)

		// reset state
		projectFile.Close()
		os.Remove(projectFileName)

		os.Remove(azdoFolder)
		os.Remove(ghFolder)
	})
}

func createPipelineManager(
	t *testing.T,
	mockContext *mocks.MockContext,
	azdContext *azdcontext.AzdContext,
	env *environment.Environment,
	args *PipelineManagerArgs,
) (*PipelineManager, error) {
	if env == nil {
		env = environment.New("test")
	}

	if args == nil {
		args = &PipelineManagerArgs{}
	}

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, env).Return(nil)

	adService := azcli.NewAdService(
		mockContext.SubscriptionCredentialProvider,
		mockContext.ArmClientOptions,
		mockContext.CoreClientOptions,
	)

	// Singletons
	ioc.RegisterInstance(mockContext.Container, *mockContext.Context)
	ioc.RegisterInstance(mockContext.Container, azdContext)
	ioc.RegisterInstance[environment.Manager](mockContext.Container, envManager)
	ioc.RegisterInstance(mockContext.Container, env)
	ioc.RegisterInstance(mockContext.Container, adService)
	ioc.RegisterInstance[account.SubscriptionCredentialProvider](
		mockContext.Container,
		mockContext.SubscriptionCredentialProvider,
	)
	mockContext.Container.MustRegisterSingleton(github.NewGitHubCli)
	mockContext.Container.MustRegisterSingleton(git.NewGitCli)

	// Pipeline providers
	pipelineProviderMap := map[string]any{
		"github-ci":  NewGitHubCiProvider,
		"github-scm": NewGitHubScmProvider,
		"azdo-ci":    NewAzdoCiProvider,
		"azdo-scm":   NewAzdoScmProvider,
	}

	for provider, constructor := range pipelineProviderMap {
		mockContext.Container.MustRegisterNamedSingleton(string(provider), constructor)
	}

	return NewPipelineManager(
		*mockContext.Context,
		envManager,
		adService,
		git.NewGitCli(mockContext.CommandRunner),
		azdContext,
		env,
		mockContext.Console,
		args,
		mockContext.Container,
		project.NewImportManager(nil),
	)
}
