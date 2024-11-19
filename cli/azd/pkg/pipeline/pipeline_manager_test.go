// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
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
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
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
		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
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

		simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)

		verifyProvider(t, manager, ciProviderGitHubActions, err)

		// Execute the initialize method, which should trigger the confirmation prompt
		err = manager.initialize(ctx, "")
		assert.NoError(t, err)

		// Check if the azure-dev.yml file was created in the expected path
		gitHubYmlPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].Files[0])
		assert.FileExists(t, gitHubYmlPath)

		deleteYamlFiles(t, tempDir)
	})

	t.Run("no files - github selected - empty workflows dir", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		deleteYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, false)

		_, err := createPipelineManager(mockContext, azdContext, nil, nil)
		// No error for GitHub, just a message to the console
		assert.NoError(t, err)
		assert.Contains(t,
			mockContext.Console.Output(),
			fmt.Sprintf(
				"%s provider selected, but no pipeline files were found in any expected directories:\n%s\n"+
					"Please add pipeline files.",
				gitHubDisplayName,
				strings.Join(pipelineProviderFiles[ciProviderGitHubActions].PipelineDirectories, "\n")))
	})

	t.Run("no files - azdo selected - empty pipelines dir", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		deleteYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, ciProviderAzureDevOps, false)

		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, fmt.Sprintf(
			"%s provider selected, but no pipeline files were found in any expected directories:\n%s\n"+
				"Please add pipeline files and try again.",
			azdoDisplayName,
			strings.Join(pipelineProviderFiles[ciProviderAzureDevOps].PipelineDirectories, "\n")))
	})

	t.Run("no files - azdo selected", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		deleteYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the confirmation prompt
		err = manager.initialize(ctx, "")
		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		// Check if the azure-dev.yml file was created in the expected path
		azdoYmlPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].Files[0])
		assert.FileExists(t, azdoYmlPath)
		deleteYamlFiles(t, tempDir)
	})

	t.Run("from persisted data azdo error", func(t *testing.T) {
		// User selects Azure DevOps, but the required directory is missing
		mockContext = resetContext(tempDir, ctx)

		envValues := map[string]string{}
		envValues[envPersistedKey] = azdoCode
		env := environment.NewWithValues("test-env", envValues)

		simulateUserInteraction(mockContext, ciProviderAzureDevOps, false)

		manager, err := createPipelineManager(mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, fmt.Sprintf(
			"%s provider selected, but no pipeline files were found in any expected directories:\n%s\n"+
				"Please add pipeline files and try again.",
			azdoDisplayName,
			strings.Join(pipelineProviderFiles[ciProviderAzureDevOps].PipelineDirectories, "\n")),
		)
	})

	t.Run("from persisted data azdo", func(t *testing.T) {
		// User has azdo persisted in env and they have the files
		mockContext = resetContext(tempDir, ctx)

		envValues := map[string]string{}
		envValues[envPersistedKey] = azdoCode
		env := environment.NewWithValues("test-env", envValues)

		simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

		manager, err := createPipelineManager(mockContext, azdContext, env, nil)
		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("from persisted data github message", func(t *testing.T) {
		// User selects Github, but the required directory is missing
		mockContext = resetContext(tempDir, ctx)

		envValues := map[string]string{}
		envValues[envPersistedKey] = gitHubCode
		env := environment.NewWithValues("test-env", envValues)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, false)

		_, err := createPipelineManager(mockContext, azdContext, env, nil)
		// No error for GitHub, just a message to the console
		assert.NoError(t, err)
		assert.Contains(t,
			mockContext.Console.Output(),
			fmt.Sprintf(
				"%s provider selected, but no pipeline files were found in any expected directories:\n%s\n"+
					"Please add pipeline files.",
				gitHubDisplayName,
				strings.Join(pipelineProviderFiles[ciProviderGitHubActions].PipelineDirectories, "\n")),
		)
	})

	t.Run("from persisted data github", func(t *testing.T) {
		// User has azdo persisted in env and they have the files
		mockContext = resetContext(tempDir, ctx)

		envValues := map[string]string{}
		envValues[envPersistedKey] = gitHubCode
		env := environment.NewWithValues("test-env", envValues)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

		manager, err := createPipelineManager(mockContext, azdContext, env, nil)

		verifyProvider(t, manager, ciProviderGitHubActions, err)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("unknown override value from arg", func(t *testing.T) {
		// User provides an invalid provider name as an argument
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

		args := &PipelineManagerArgs{
			PipelineProvider: "other",
		}

		manager, err := createPipelineManager(mockContext, azdContext, nil, args)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "invalid ci provider type other")

		deleteYamlFiles(t, tempDir)
	})
	t.Run("unknown override value from env", func(t *testing.T) {
		// User provides an invalid provider name in env
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

		envValues := map[string]string{}
		envValues[envPersistedKey] = "other"
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "invalid ci provider type other")

		deleteYamlFiles(t, tempDir)
	})
	t.Run("unknown override value from yaml", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: other")

		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "invalid ci provider type other")

		resetAzureYaml(t, projectFileName)
		deleteYamlFiles(t, tempDir)
	})
	t.Run("override persisted value with yaml", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: fromYaml")

		envValues := map[string]string{}
		envValues[envPersistedKey] = "persisted"
		env := environment.NewWithValues("test-env", envValues)

		manager, err := createPipelineManager(mockContext, azdContext, env, nil)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "invalid ci provider type fromYaml")

		resetAzureYaml(t, projectFileName)
		deleteYamlFiles(t, tempDir)
	})
	t.Run("override persisted and yaml with arg", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: fromYaml")

		envValues := map[string]string{}
		envValues[envPersistedKey] = "persisted"
		env := environment.NewWithValues("test-env", envValues)
		args := &PipelineManagerArgs{
			PipelineProvider: "arg",
		}

		manager, err := createPipelineManager(mockContext, azdContext, env, args)
		assert.Nil(t, manager)
		assert.EqualError(t, err, "invalid ci provider type arg")

		resetAzureYaml(t, projectFileName)
		deleteYamlFiles(t, tempDir)
	})
	t.Run("github directory only", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)

		verifyProvider(t, manager, ciProviderGitHubActions, err)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("azdo directory only", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)

		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("both files - user selects GitHub", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		createYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, true)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the provider selection prompt
		err = manager.initialize(ctx, "")

		verifyProvider(t, manager, ciProviderGitHubActions, err)

		deleteYamlFiles(t, tempDir)
	})

	t.Run("has azure-dev.yaml github", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		createYamlFiles(t, tempDir, ciProviderGitHubActions, 1)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the provider selection prompt
		err = manager.initialize(ctx, "")

		verifyProvider(t, manager, ciProviderGitHubActions, err)

		deleteYamlFiles(t, tempDir)
	})

	t.Run("has azure-dev.yaml azdo", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		createYamlFiles(t, tempDir, ciProviderAzureDevOps, 1)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the provider selection prompt
		err = manager.initialize(ctx, "")

		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		deleteYamlFiles(t, tempDir)
	})

	t.Run("has azure-dev.yaml in .azuredevops folder", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		// Create the azure-dev.yml file in the .azuredevops folder using createYamlFiles
		createYamlFiles(t, tempDir, ciProviderAzureDevOps, 2)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the provider selection prompt
		err = manager.initialize(ctx, "")

		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		deleteYamlFiles(t, tempDir)
	})

	t.Run("both files - user selects azdo", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		createYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

		// Initialize the PipelineManager
		manager, err := createPipelineManager(mockContext, azdContext, nil, nil)
		assert.NotNil(t, manager)
		assert.NoError(t, err)

		// Execute the initialize method, which should trigger the provider selection prompt
		err = manager.initialize(ctx, "")

		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		deleteYamlFiles(t, tempDir)
	})

	t.Run("persist selection on environment", func(t *testing.T) {

		mockContext = resetContext(tempDir, ctx)

		simulateUserInteraction(mockContext, ciProviderAzureDevOps, true)

		env := environment.New("test")
		args := &PipelineManagerArgs{
			PipelineProvider: azdoCode,
		}

		manager, err := createPipelineManager(mockContext, azdContext, env, args)

		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		envValue, found := env.Dotenv()[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, ciProviderType(envValue), ciProviderAzureDevOps)

		// Calling function again with same env and without override arg should use the persisted
		err = manager.initialize(*mockContext.Context, "")

		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		deleteYamlFiles(t, tempDir)
	})
	t.Run("persist selection on environment and override with yaml", func(t *testing.T) {

		mockContext = resetContext(tempDir, ctx)

		createYamlFiles(t, tempDir)

		env := environment.New("test")
		args := &PipelineManagerArgs{
			PipelineProvider: azdoCode,
		}
		manager, err := createPipelineManager(mockContext, azdContext, env, args)

		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		// Calling function again with same env and without override arg should use the persisted
		err = manager.initialize(*mockContext.Context, "")

		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		// Write yaml to override
		appendToAzureYaml(t, projectFileName, "pipeline:\n\r  provider: github")

		// Calling function again with same env and without override arg should detect yaml change and override persisted
		err = manager.initialize(*mockContext.Context, "")

		verifyProvider(t, manager, ciProviderGitHubActions, err)

		// the persisted choice should be updated based on the value set on yaml
		envValue, found := env.Dotenv()[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, ciProviderType(envValue), ciProviderGitHubActions)

		// Call again to check persisted(github) after one change (and yaml is still present)
		err = manager.initialize(*mockContext.Context, "")
		verifyProvider(t, manager, ciProviderGitHubActions, err)

		// Check argument override having yaml(github) config and persisted config(github)
		expected := azdoCode
		err = manager.initialize(*mockContext.Context, expected)
		verifyProvider(t, manager, ciProviderAzureDevOps, err)

		// the persisted selection is now azdo(env) but yaml is github
		envValue, found = env.Dotenv()[envPersistedKey]
		assert.True(t, found)
		assert.Equal(t, expected, envValue)

		// persisted = azdo (per last run) and yaml = github, should return github
		// as yaml overrides a persisted run
		err = manager.initialize(*mockContext.Context, "")
		verifyProvider(t, manager, ciProviderGitHubActions, err)

		// reset state
		resetAzureYaml(t, projectFileName)

		deleteYamlFiles(t, tempDir)
	})
}

func Test_promptForCiFiles(t *testing.T) {
	t.Run("no files - github selected - no app host - fed Cred", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:    ciProviderGitHubActions,
			InfraProvider: infraProviderBicep,
			RepoRoot:      tempDir,
			HasAppHost:    false,
			BranchName:    "main",
			AuthType:      AuthTypeFederated,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
	t.Run("no files - github selected - App host - fed Cred", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:    ciProviderGitHubActions,
			InfraProvider: infraProviderBicep,
			RepoRoot:      tempDir,
			HasAppHost:    true,
			BranchName:    "main",
			AuthType:      AuthTypeFederated,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
	t.Run("no files - azdo selected - App host - fed Cred", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:    ciProviderAzureDevOps,
			InfraProvider: infraProviderBicep,
			RepoRoot:      tempDir,
			HasAppHost:    true,
			BranchName:    "main",
			AuthType:      AuthTypeFederated,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
	t.Run("no files - github selected - no app host - client cred", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:    ciProviderGitHubActions,
			InfraProvider: infraProviderBicep,
			RepoRoot:      tempDir,
			HasAppHost:    false,
			BranchName:    "main",
			AuthType:      AuthTypeClientCredentials,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
	t.Run("no files - github selected - branch name", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:    ciProviderGitHubActions,
			InfraProvider: infraProviderBicep,
			RepoRoot:      tempDir,
			HasAppHost:    false,
			BranchName:    "non-main",
			AuthType:      AuthTypeFederated,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
	t.Run("no files - azdo selected - no app host - fed Cred", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:    ciProviderAzureDevOps,
			InfraProvider: infraProviderBicep,
			RepoRoot:      tempDir,
			HasAppHost:    false,
			BranchName:    "main",
			AuthType:      AuthTypeFederated,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
	t.Run("no files - azdo selected - no app host - client cred", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:    ciProviderAzureDevOps,
			InfraProvider: infraProviderBicep,
			RepoRoot:      tempDir,
			HasAppHost:    false,
			BranchName:    "main",
			AuthType:      AuthTypeClientCredentials,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
	t.Run("no files - azdo selected - branch name", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:    ciProviderAzureDevOps,
			InfraProvider: infraProviderBicep,
			RepoRoot:      tempDir,
			HasAppHost:    false,
			BranchName:    "non-main",
			AuthType:      AuthTypeFederated,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
}

func Test_promptForCiFiles_azureDevOpsDirectory(t *testing.T) {
	t.Run("no files - azdo selected - azuredevops dir - fed Cred", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, ".azuredevops")
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, ".azuredevops/azure-dev.yml")
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:    ciProviderAzureDevOps,
			InfraProvider: infraProviderBicep,
			RepoRoot:      tempDir,
			HasAppHost:    false,
			BranchName:    "main",
			AuthType:      AuthTypeFederated,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})

	t.Run("no files - azdo selected - azuredevops dir - client cred", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, ".azuredevops")
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, ".azuredevops/azure-dev.yml")
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:    ciProviderAzureDevOps,
			InfraProvider: infraProviderBicep,
			RepoRoot:      tempDir,
			HasAppHost:    false,
			BranchName:    "main",
			AuthType:      AuthTypeClientCredentials,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
}

func createPipelineManager(
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
	mockContext.Container.MustRegisterSingleton(git.NewCli)

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
		git.NewCli(mockContext.CommandRunner),
		azdContext,
		env,
		mockContext.Console,
		args,
		mockContext.Container,
		project.NewImportManager(
			project.NewDotNetImporter(nil, nil, nil, nil, mockContext.AlphaFeaturesManager),
			mockinput.NewMockConsole()),
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
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "branch --show-current")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "main", ""), nil
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

func createYamlFiles(t *testing.T, tempDir string, createOptionsAndFileIndex ...interface{}) {
	shouldCreateGitHub := true
	shouldCreateAzdo := true
	fileIndex := 0

	// Determine which providers to create files for and if a fileIndex is provided
	for _, option := range createOptionsAndFileIndex {
		switch v := option.(type) {
		case ciProviderType:
			switch v {
			case ciProviderGitHubActions:
				shouldCreateGitHub = true
				shouldCreateAzdo = false
			case ciProviderAzureDevOps:
				shouldCreateAzdo = true
				shouldCreateGitHub = false
			}
		case int:
			fileIndex = v
		}
	}

	if shouldCreateGitHub {
		createPipelineFiles(t, tempDir, ciProviderGitHubActions, fileIndex)
	}

	if shouldCreateAzdo {
		createPipelineFiles(t, tempDir, ciProviderAzureDevOps, fileIndex)
	}
}

// Helper function to create pipeline files
func createPipelineFiles(t *testing.T, baseDir string, provider ciProviderType, fileIndex int) {
	files := pipelineProviderFiles[provider].Files
	if fileIndex < 0 || fileIndex >= len(files) {
		fileIndex = 0 // Default to 0 if index is out of bounds
	}

	// Get the full path by joining the baseDir with the relative file path
	filePath := filepath.Join(baseDir, files[fileIndex])

	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	err := os.MkdirAll(dir, osutil.PermissionDirectory)
	assert.NoError(t, err)

	// Create the file
	file, err := os.Create(filePath)
	assert.NoError(t, err)
	err = file.Close()
	assert.NoError(t, err)
}

func deleteYamlFiles(t *testing.T, tempDir string, deleteOptions ...ciProviderType) {
	shouldDeleteGitHub := true
	shouldDeleteAzdo := true

	if len(deleteOptions) > 0 {
		shouldDeleteGitHub = false
		shouldDeleteAzdo = false
		for _, option := range deleteOptions {
			switch option {
			case ciProviderGitHubActions:
				shouldDeleteGitHub = true
			case ciProviderAzureDevOps:
				shouldDeleteAzdo = true
			}
		}
	}

	if shouldDeleteGitHub {
		deletePipelineFiles(t, tempDir, ciProviderGitHubActions)
	}

	if shouldDeleteAzdo {
		deletePipelineFiles(t, tempDir, ciProviderAzureDevOps)
	}
}

// Helper function to delete pipeline files and directories
func deletePipelineFiles(t *testing.T, baseDir string, provider ciProviderType) {
	for _, file := range pipelineProviderFiles[provider].Files {
		fullPath := filepath.Join(baseDir, file)
		err := os.RemoveAll(fullPath)
		assert.NoError(t, err)
	}
}

func simulateUserInteraction(mockContext *mocks.MockContext, providerLabel ciProviderType, createConfirmation bool) {
	var providerIndex int

	switch providerLabel {
	case ciProviderGitHubActions:
		providerIndex = 0
	case ciProviderAzureDevOps:
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

func verifyProvider(t *testing.T, manager *PipelineManager, providerLabel ciProviderType, err error) {
	assert.NoError(t, err)

	switch providerLabel {
	case ciProviderGitHubActions:
		assert.IsType(t, &GitHubScmProvider{}, manager.scmProvider)
		assert.IsType(t, &GitHubCiProvider{}, manager.ciProvider)
	case ciProviderAzureDevOps:
		assert.IsType(t, &AzdoScmProvider{}, manager.scmProvider)
		assert.IsType(t, &AzdoCiProvider{}, manager.ciProvider)
	default:
		t.Fatalf("%s is not a known pipeline provider", providerLabel)
	}
}

func normalizeEOL(input []byte) string {
	return strings.ReplaceAll(string(input), "\r\n", "\n")
}
