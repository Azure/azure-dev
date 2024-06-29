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
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
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

		deleteYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, gitHubLabel, true)

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)

		verifyProvider(t, manager, gitHubLabel, err)

		// Execute the initialize method, which should trigger the confirmation prompt
		err = manager.initialize(ctx, "")
		assert.NoError(t, err)

		// Check if the azure-dev.yml file was created in the expected path
		gitHubYmlPath := filepath.Join(tempDir, gitHubYml)
		assert.FileExists(t, gitHubYmlPath)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("no files - github selected - empty workflows dir", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		deleteYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, gitHubLabel, false)

		_, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		// No error for GitHub, just a message to the console
		assert.NoError(t, err)
		assert.Contains(t,
			mockContext.Console.Output(), fmt.Sprintf("%s provider selected, but %s is empty. Please add pipeline files.",
				gitHubDisplayName, gitHubWorkflowsDirectory))
	})
	t.Run("no files - azdo selected - empty pipelines dir", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		deleteYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, azdoLabel, false)

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, fmt.Sprintf("%s provider selected, but %s is empty. Please add pipeline files and try again.",
			azdoDisplayName, azdoPipelinesDirectory))
	})
	t.Run("no files - azdo selected", func(t *testing.T) {

		mockContext = resetContext(tempDir, ctx)

		deleteYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, azdoLabel, true)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the confirmation prompt
		err = manager.initialize(ctx, "")
		verifyProvider(t, manager, azdoLabel, err)

		// Check if the azure-dev.yml file was created in the expected path
		azdoYmlPath := filepath.Join(tempDir, azdoYml)
		assert.FileExists(t, azdoYmlPath)
		deleteYamlFiles(t, tempDir)
	})
	t.Run("from persisted data azdo error", func(t *testing.T) {
		// User selects Azure DevOps, but the required directory is missing
		mockContext = resetContext(tempDir, ctx)

		envValues := map[string]string{}
		envValues[envPersistedKey] = azdoLabel
		env := environment.NewWithValues("test-env", envValues)

		simulateUserInteraction(mockContext, azdoLabel, false)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, fmt.Sprintf("%s provider selected, but %s is empty. Please add pipeline files and try again.",
			azdoDisplayName, azdoPipelinesDirectory))
	})
	t.Run("from persisted data azdo", func(t *testing.T) {
		// User has azdo persisted in env and they have the files
		mockContext = resetContext(tempDir, ctx)

		envValues := map[string]string{}
		envValues[envPersistedKey] = azdoLabel
		env := environment.NewWithValues("test-env", envValues)

		simulateUserInteraction(mockContext, azdoLabel, true)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		verifyProvider(t, manager, azdoLabel, err)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("from persisted data github message", func(t *testing.T) {
		// User selects Github, but the required directory is missing
		mockContext = resetContext(tempDir, ctx)

		envValues := map[string]string{}
		envValues[envPersistedKey] = gitHubLabel
		env := environment.NewWithValues("test-env", envValues)

		simulateUserInteraction(mockContext, gitHubLabel, false)

		_, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		// No error for GitHub, just a message to the console
		assert.NoError(t, err)
		assert.Contains(t,
			mockContext.Console.Output(), fmt.Sprintf("%s provider selected, but %s is empty. Please add pipeline files.",
				gitHubDisplayName, gitHubWorkflowsDirectory))
	})
	t.Run("from persisted data github", func(t *testing.T) {
		// User has azdo persisted in env and they have the files
		mockContext = resetContext(tempDir, ctx)

		envValues := map[string]string{}
		envValues[envPersistedKey] = gitHubLabel
		env := environment.NewWithValues("test-env", envValues)

		simulateUserInteraction(mockContext, gitHubLabel, true)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)

		verifyProvider(t, manager, gitHubLabel, err)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("unknown override value from arg", func(t *testing.T) {
		// User provides an invalid provider name as an argument
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, gitHubLabel, true)

		args := &PipelineManagerArgs{
			PipelineProvider: "other",
		}

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, args)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "other is not a known pipeline provider")

		deleteYamlFiles(t, tempDir)
	})
	t.Run("unknown override value from env", func(t *testing.T) {
		// User provides an invalid provider name in env
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, gitHubLabel, true)

		envValues := map[string]string{}
		envValues[envPersistedKey] = "other"
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "other is not a known pipeline provider")

		deleteYamlFiles(t, tempDir)
	})
	t.Run("unknown override value from yaml", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, gitHubLabel, true)

		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: other")

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "other is not a known pipeline provider")

		resetAzureYaml(t, projectFileName)
		deleteYamlFiles(t, tempDir)
	})
	t.Run("override persisted value with yaml", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, gitHubLabel, true)

		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: fromYaml")

		envValues := map[string]string{}
		envValues[envPersistedKey] = "persisted"
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(t, mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "fromYaml is not a known pipeline provider")

		resetAzureYaml(t, projectFileName)
		deleteYamlFiles(t, tempDir)
	})
	t.Run("override persisted and yaml with arg", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, gitHubLabel, true)

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

		resetAzureYaml(t, projectFileName)
		deleteYamlFiles(t, tempDir)
	})
	t.Run("github directory only", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, gitHubLabel, true)

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)

		verifyProvider(t, manager, gitHubLabel, err)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("azdo directory only", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, azdoLabel, true)

		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)

		verifyProvider(t, manager, azdoLabel, err)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("both files - user selects GitHub", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		createYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, gitHubLabel, true)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the provider selection prompt
		err = manager.initialize(ctx, "")

		verifyProvider(t, manager, gitHubLabel, err)

		deleteYamlFiles(t, tempDir)
	})

	t.Run("both files - user selects azdo", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		createYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, azdoLabel, true)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(t, mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the provider selection prompt
		err = manager.initialize(ctx, "")

		verifyProvider(t, manager, azdoLabel, err)

		deleteYamlFiles(t, tempDir)
	})

	t.Run("persist selection on environment", func(t *testing.T) {

		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, azdoLabel, true)

		env := environment.New("test")
		args := &PipelineManagerArgs{
			PipelineProvider: azdoLabel,
		}

		manager, err := createPipelineManager(t, mockContext, azdContext, env, args)

		verifyProvider(t, manager, azdoLabel, err)

		envValue, found := env.Dotenv()[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, azdoLabel, envValue)

		// Calling function again with same env and without override arg should use the persisted
		err = manager.initialize(*mockContext.Context, "")

		verifyProvider(t, manager, azdoLabel, err)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("persist selection on environment and override with yaml", func(t *testing.T) {

		mockContext = resetContext(tempDir, ctx)

		createYamlFiles(t, tempDir)

		env := environment.New("test")
		args := &PipelineManagerArgs{
			PipelineProvider: azdoLabel,
		}
		manager, err := createPipelineManager(t, mockContext, azdContext, env, args)

		verifyProvider(t, manager, azdoLabel, err)

		// Calling function again with same env and without override arg should use the persisted
		err = manager.initialize(*mockContext.Context, "")

		verifyProvider(t, manager, azdoLabel, err)

		// Write yaml to override
		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: github")

		// Calling function again with same env and without override arg should detect yaml change and override persisted
		err = manager.initialize(*mockContext.Context, "")

		verifyProvider(t, manager, gitHubLabel, err)

		// the persisted choice should be updated based on the value set on yaml
		envValue, found := env.Dotenv()[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, gitHubLabel, envValue)

		// Call again to check persisted(github) after one change (and yaml is still present)
		err = manager.initialize(*mockContext.Context, "")
		verifyProvider(t, manager, gitHubLabel, err)

		// Check argument override having yaml(github) config and persisted config(github)
		err = manager.initialize(*mockContext.Context, azdoLabel)
		verifyProvider(t, manager, azdoLabel, err)

		// the persisted selection is now azdo(env) but yaml is github
		envValue, found = env.Dotenv()[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, azdoLabel, envValue)

		// persisted = azdo (per last run) and yaml = github, should return github
		// as yaml overrides a persisted run
		err = manager.initialize(*mockContext.Context, "")
		verifyProvider(t, manager, gitHubLabel, err)

		// reset state
		resetAzureYaml(t, projectFileName)

		deleteYamlFiles(t, tempDir)
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

	entraIdService := entraid.NewEntraIdService(
		mockContext.SubscriptionCredentialProvider,
		mockContext.ArmClientOptions,
		mockContext.CoreClientOptions,
	)

	// Singletons
	ioc.RegisterInstance(mockContext.Container, *mockContext.Context)
	ioc.RegisterInstance(mockContext.Container, azdContext)
	ioc.RegisterInstance[environment.Manager](mockContext.Container, envManager)
	ioc.RegisterInstance(mockContext.Container, env)
	ioc.RegisterInstance(mockContext.Container, entraIdService)
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
		entraIdService,
		git.NewGitCli(mockContext.CommandRunner),
		azdContext,
		env,
		mockContext.Console,
		args,
		mockContext.Container,
		project.NewImportManager(nil),
		&mockUserConfigManager{},
	)
}

type mockUserConfigManager struct {
}

func (m *mockUserConfigManager) Load() (config.Config, error) {
	return config.NewEmptyConfig(), nil
}

func (m *mockUserConfigManager) Save(c config.Config) error {
	return nil
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

	// Write the default content to the file
	_, err = projectFile.WriteString(defaultContent)
	if err != nil {
		projectFile.Close() // Ensure the file is closed before handling the error
		t.Fatalf("Failed to write default content to azure.yaml file: %v", err)
	}

	err = projectFile.Close()
	if err != nil {
		t.Fatalf("Failed to close azure.yaml file: %v", err)
	}
}

func appendToAzureYaml(t *testing.T, projectFilePath string, content string) {
	// Open the file in append mode
	projectFile, err := os.OpenFile(projectFilePath, os.O_APPEND|os.O_WRONLY, osutil.PermissionFile)
	if err != nil {
		t.Fatalf("Failed to open azure.yaml file for appending: %v", err)
	}

	// Append the provided content to the file
	_, err = projectFile.WriteString(content)
	if err != nil {
		projectFile.Close() // Ensure the file is closed before handling the error
		t.Fatalf("Failed to append content to azure.yaml file: %v", err)
	}

	err = projectFile.Close()
	if err != nil {
		t.Fatalf("Failed to close azure.yaml file: %v", err)
	}
}

func resetContext(tempDir string, ctx context.Context) *mocks.MockContext {
	newMockContext := mocks.NewMockContext(ctx)
	setupGithubCliMocks(newMockContext)
	setupGitCliMocks(newMockContext, tempDir)
	return newMockContext
}

func createYamlFiles(t *testing.T, tempDir string, createOptions ...string) {
	shouldCreateGitHub := true
	shouldCreateAzdo := true

	if len(createOptions) > 0 {
		shouldCreateGitHub = false
		shouldCreateAzdo = false
		for _, option := range createOptions {
			switch option {
			case gitHubLabel:
				shouldCreateGitHub = true
			case azdoLabel:
				shouldCreateAzdo = true
			}
		}
	}

	if shouldCreateGitHub {
		// Create the GitHub Actions directory and file
		ghDirectory := filepath.Join(tempDir, gitHubWorkflowsDirectory)
		err := os.MkdirAll(ghDirectory, osutil.PermissionDirectory)
		assert.NoError(t, err)
		ghYmlFile := filepath.Join(ghDirectory, defaultPipelineFileName)
		file, err := os.Create(ghYmlFile)
		assert.NoError(t, err)
		err = file.Close()
		assert.NoError(t, err)
	}

	if shouldCreateAzdo {
		// Create the Azure DevOps directory and file
		azdoDirectory := filepath.Join(tempDir, azdoPipelinesDirectory)
		err := os.MkdirAll(azdoDirectory, osutil.PermissionDirectory)
		assert.NoError(t, err)
		azdoYmlFile := filepath.Join(azdoDirectory, defaultPipelineFileName)
		file, err := os.Create(azdoYmlFile)
		assert.NoError(t, err)
		err = file.Close()
		assert.NoError(t, err)
	}
}

func deleteYamlFiles(t *testing.T, tempDir string, deleteOptions ...string) {
	shouldDeleteGitHub := true
	shouldDeleteAzdo := true

	if len(deleteOptions) > 0 {
		shouldDeleteGitHub = false
		shouldDeleteAzdo = false
		for _, option := range deleteOptions {
			switch option {
			case gitHubLabel:
				shouldDeleteGitHub = true
			case azdoLabel:
				shouldDeleteAzdo = true
			}
		}
	}

	if shouldDeleteGitHub {
		// Delete the GitHub Actions directory and file
		ghDirectory := filepath.Join(tempDir, gitHubWorkflowsDirectory)
		err := os.RemoveAll(ghDirectory)
		assert.NoError(t, err)
	}

	if shouldDeleteAzdo {
		// Delete the Azure DevOps directory and file
		azdoDirectory := filepath.Join(tempDir, azdoPipelinesDirectory)
		err := os.RemoveAll(azdoDirectory)
		assert.NoError(t, err)
	}
}

func simulateUserInteraction(mockContext *mocks.MockContext, providerLabel string, createConfirmation bool) {
	var providerIndex int

	switch providerLabel {
	case gitHubLabel:
		providerIndex = 0
	case azdoLabel:
		providerIndex = 1
	default:
		providerIndex = 0
	}

	// Simulate the user selecting the CI/CD provider
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Select a provider:")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return providerIndex, nil
	})

	// Simulate the user responding to the creation confirmation
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Would you like")
	}).Respond(createConfirmation)
}

func verifyProvider(t *testing.T, manager *PipelineManager, providerLabel string, err error) {
	assert.NoError(t, err)

	switch providerLabel {
	case gitHubLabel:
		assert.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, manager.ciProvider)
	case azdoLabel:
		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)
	default:
		t.Fatalf("%s is not a known pipeline provider", providerLabel)
	}
}
