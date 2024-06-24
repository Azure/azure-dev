// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
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
	mockContext := resetContext(tempDir, ctx)

	//1. Test without a project file
	t.Run("can't load project settings", func(t *testing.T) {
		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.Nil(t, manager)
		assert.ErrorContains(
			t, err, "Loading project configuration: reading project file:")
	})

	//2. Then create the project file
	projectFileName := filepath.Join(tempDir, "azure.yaml")
	resetAzureYaml(t, projectFileName)

	t.Run("no files - github selected", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		tmpAzdoFolder := filepath.Join(tempDir, azdoFolder)
		os.RemoveAll(tmpAzdoFolder)
		tmpGithubFolder := filepath.Join(tempDir, gitHubFolder)
		os.RemoveAll(tmpGithubFolder)

		// Simulate the user confirming the creation of missing CI/CD files
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Which provider would you like to set up?")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			// Select the first from the list
			return 0, nil
		})

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Would you like to create the")
		}).Respond(true)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		assert.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, manager.ciProvider)

		// Execute the initialize method, which should trigger the confirmation prompt
		err = manager.initialize(ctx, "")
		assert.NoError(t, err)

		// Check if the azure-dev.yml file was created in the expected path
		gitHubYmlPath := filepath.Join(tempDir, gitHubYml)
		assert.FileExists(t, gitHubYmlPath)
		os.RemoveAll(tmpGithubFolder)
	})
	t.Run("no files - azdo selected", func(t *testing.T) {

		mockContext = resetContext(tempDir, ctx)

		tmpAzdoFolder := filepath.Join(tempDir, azdoFolder)
		os.RemoveAll(tmpAzdoFolder)
		tmpGithubFolder := filepath.Join(tempDir, gitHubFolder)
		os.RemoveAll(tmpGithubFolder)

		// Simulate the user confirming the creation of missing CI/CD files
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Which provider would you like to set up?")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 1, nil
		})

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Would you like to create the")
		}).Respond(true)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the confirmation prompt
		err = manager.initialize(ctx, "")
		assert.NoError(t, err)

		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)

		// Check if the azure-dev.yml file was created in the expected path
		azdoYmlPath := filepath.Join(tempDir, azdoYml)
		assert.FileExists(t, azdoYmlPath)
		os.RemoveAll(tmpAzdoFolder)
	})
	t.Run("from persisted data azdo error", func(t *testing.T) {
		// User selects Azure DevOps, but the required folder is missing

		mockContext = resetContext(tempDir, ctx)

		envValues := map[string]string{}
		envValues[envPersistedKey] = azdoLabel
		env := environment.NewWithValues("test-env", envValues)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Would you like to create the")
		}).Respond(false)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		//assert.NoError(t, err)

		//err = manager.initialize(ctx, "")
		assert.EqualError(t, err, fmt.Sprintf("%s provider selected, but %s is empty. Please add pipeline files.",
			azdoLabel, azdoPipelinesFolder))
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
			gitHubWorkflowsFolder))

		os.Remove(azdoFolder)
	})
	t.Run("from persisted data github", func(t *testing.T) {
		azdoFolder := filepath.Join(tempDir, gitHubWorkflowsFolder)
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
		ghFolder := filepath.Join(tempDir, gitHubWorkflowsFolder)
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
		ghFolder := filepath.Join(tempDir, gitHubWorkflowsFolder)
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
		ghFolder := filepath.Join(tempDir, gitHubWorkflowsFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: other")

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "other is not a known pipeline provider")

		// Remove folder - reset state
		os.Remove(ghFolder)
		resetAzureYaml(t, projectFileName)
	})
	t.Run("override persisted value with yaml", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, gitHubWorkflowsFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: fromYaml")

		envValues := map[string]string{}
		envValues[envPersistedKey] = "persisted"
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "fromYaml is not a known pipeline provider")

		// Remove folder - reset state
		os.Remove(ghFolder)
		resetAzureYaml(t, projectFileName)
	})
	t.Run("override persisted and yaml with arg", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, gitHubWorkflowsFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: fromYaml")

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
		resetAzureYaml(t, projectFileName)
	})
	t.Run("github folder only", func(t *testing.T) {
		ghFolder := filepath.Join(tempDir, gitHubWorkflowsFolder)
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
	t.Run("both files - user selects GitHub", func(t *testing.T) {
		// Create both GitHub and AzDO folders and YAML files
		ghFolder := filepath.Join(tempDir, gitHubWorkflowsFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		azdoFolder := filepath.Join(tempDir, azdoFolder)
		err = os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		// Create the azure-dev.yml file in the GitHub folder
		ghYmlFile := filepath.Join(ghFolder, defaultPipelineFileName)
		err = os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		_, err = os.Create(ghYmlFile)
		assert.NoError(t, err)

		// Create the azure-dev.yml file in the Azure DevOps folder
		azdoYmlFile := filepath.Join(azdoFolder, defaultPipelineFileName)
		err = os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		_, err = os.Create(azdoYmlFile)
		assert.NoError(t, err)

		// Simulate the user selecting GitHub Actions
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Which provider would you like to set up?")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 0, nil // Select GitHub Actions
		})

		// Initialize the PipelineManager
		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the provider selection prompt
		err = manager.initialize(ctx, "")
		assert.NoError(t, err)

		// Verify if the correct provider was chosen
		assert.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, manager.ciProvider)

		// Remove folders to reset state
		os.RemoveAll(ghFolder)
		os.RemoveAll(azdoFolder)
	})

	t.Run("both files - user selects AzDO", func(t *testing.T) {
		// Create both GitHub and AzDO folders and YAML files
		ghFolder := filepath.Join(tempDir, gitHubFolder)
		err := os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		azdoFolder := filepath.Join(tempDir, azdoFolder)
		err = os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)

		// Create the azure-dev.yml file in the GitHub folder
		ghYmlFile := filepath.Join(ghFolder, defaultPipelineFileName)
		err = os.MkdirAll(ghFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		_, err = os.Create(ghYmlFile)
		assert.NoError(t, err)

		// Create the azure-dev.yml file in the Azure DevOps folder
		azdoYmlFile := filepath.Join(azdoFolder, defaultPipelineFileName)
		err = os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		_, err = os.Create(azdoYmlFile)
		assert.NoError(t, err)

		// Simulate the user selecting Azure DevOps
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Which provider would you like to set up?")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 1, nil // Select Azure DevOps
		})

		// Initialize the PipelineManager
		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the provider selection prompt
		err = manager.initialize(ctx, "")
		assert.NoError(t, err)

		// Verify if the correct provider was chosen
		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)

		// Remove folders to reset state
		os.RemoveAll(ghFolder)
		os.RemoveAll(azdoFolder)
	})

	t.Run("persist selection on environment", func(t *testing.T) {
		azdoFolder := filepath.Join(tempDir, azdoFolder)
		err := os.MkdirAll(azdoFolder, osutil.PermissionDirectory)
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
		tmpAzdoFolder := filepath.Join(tempDir, azdoFolder)
		err := os.MkdirAll(tmpAzdoFolder, osutil.PermissionDirectory)
		assert.NoError(t, err)
		tmpGitHubFolder := filepath.Join(tempDir, gitHubWorkflowsFolder)
		err = os.MkdirAll(tmpGitHubFolder, osutil.PermissionDirectory)
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

		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: github")

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
		resetAzureYaml(t, projectFileName)

		os.Remove(tmpAzdoFolder)
		os.Remove(tmpGitHubFolder)
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

func setupGitCliMocks(mockContext *mocks.MockContext, repoPath string) {
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "rev-parse --show-toplevel")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, repoPath, ""), nil
	})
}

func resetAzureYaml(t *testing.T, projectFilePath string) {
	// Default content to write
	defaultContent := "name: test\n"

	// Create or reset the file with default content
	projectFile, err := os.Create(projectFilePath)
	if err != nil {
		t.Fatalf("Failed to create or reset azure.yaml file: %v", err)
	}
	defer projectFile.Close()

	// Write the default content to the file
	_, err = projectFile.WriteString(defaultContent)
	if err != nil {
		t.Fatalf("Failed to write default content to azure.yaml file: %v", err)
	}
}

func appendToAzureYaml(t *testing.T, projectFilePath string, content string) {
	// Open the file in append mode
	projectFile, err := os.OpenFile(projectFilePath, os.O_APPEND|os.O_WRONLY, osutil.PermissionFile)
	if err != nil {
		t.Fatalf("Failed to open azure.yaml file for appending: %v", err)
	}
	defer projectFile.Close()

	// Append the provided content to the file
	_, err = projectFile.WriteString(content)
	if err != nil {
		t.Fatalf("Failed to append content to azure.yaml file: %v", err)
	}
}

func resetContext(tempDir string, ctx context.Context) *mocks.MockContext {
	newMockContext := mocks.NewMockContext(ctx)
	setupGithubCliMocks(newMockContext)
	setupGitCliMocks(newMockContext, tempDir)
	return newMockContext
}
