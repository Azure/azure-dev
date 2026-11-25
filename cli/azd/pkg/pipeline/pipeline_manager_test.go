// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	msi "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/armmsi"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_PipelineManager_Initialize(t *testing.T) {
	tempDir := t.TempDir()
	ctx := t.Context()
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
		manager.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		manager.configOptions = &configurePipelineOptions{}
		err = manager.ensurePipelineDefinition(ctx, AuthTypeFederated)
		assert.NoError(t, err)

		// Check if the azure-dev.yml file was created in the expected path
		gitHubYmlPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].Files[0])
		assert.FileExists(t, gitHubYmlPath)
		assert.Equal(t, filepath.Base(gitHubYmlPath), manager.configOptions.pipelineFile)

		deleteYamlFiles(t, tempDir)
	})

	t.Run("no files - github selected - empty workflows dir", func(t *testing.T) {
		mockContext = resetContext(tempDir, ctx)

		deleteYamlFiles(t, tempDir)

		simulateUserInteraction(mockContext, ciProviderGitHubActions, false)

		m, err := createPipelineManager(mockContext, azdContext, nil, nil)
		// No error for GitHub, just a message to the console
		assert.NoError(t, err)
		m.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		m.configOptions = &configurePipelineOptions{}
		err = m.ensurePipelineDefinition(ctx, AuthTypeFederated)
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

		m, err := createPipelineManager(mockContext, azdContext, nil, nil)
		assert.NoError(t, err)
		m.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		m.configOptions = &configurePipelineOptions{}
		err = m.ensurePipelineDefinition(ctx, AuthTypeFederated)
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
		assert.NoError(t, err)
		manager.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		manager.configOptions = &configurePipelineOptions{}
		err = manager.ensurePipelineDefinition(ctx, AuthTypeFederated)
		assert.NoError(t, err)
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
		assert.NoError(t, err)
		manager.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		manager.configOptions = &configurePipelineOptions{}
		err = manager.ensurePipelineDefinition(ctx, AuthTypeFederated)
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

		m, err := createPipelineManager(mockContext, azdContext, env, nil)
		// No error for GitHub, just a message to the console
		assert.NoError(t, err)
		m.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		m.configOptions = &configurePipelineOptions{}
		err = m.ensurePipelineDefinition(ctx, AuthTypeFederated)
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

		manager.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		manager.configOptions = &configurePipelineOptions{}

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

		manager.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		manager.configOptions = &configurePipelineOptions{}

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

		manager.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		manager.configOptions = &configurePipelineOptions{}

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

		manager.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		manager.configOptions = &configurePipelineOptions{}

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

		manager.infra = &project.Infra{
			Options: provisioning.Options{
				Provider: provisioning.Bicep,
			},
			IsCompose: false}
		manager.configOptions = &configurePipelineOptions{}

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
	t.Run("required extensions are installed for github", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].PipelineDirectories[0])
		require.NoError(t, os.MkdirAll(path, osutil.PermissionDirectory))
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].Files[0])

		err := generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   tempDir,
			BranchName: "main",
			AuthType:   AuthTypeFederated,
			RequiredExtensions: []RequiredExtension{
				{Id: "azure.ai.agents", Version: "1.0.0-beta.9"},
				{Id: "azure.ai.projects", Version: "1.0.0-beta.10"},
			},
		})

		require.NoError(t, err)
		content, err := os.ReadFile(expectedPath)
		require.NoError(t, err)
		generated := string(content)
		assert.Contains(t, generated, "Install required azd extensions")
		assert.Contains(t, generated,
			`azd extension install "azure.ai.agents" --version "1.0.0-beta.9" --force --no-prompt`)
		assert.Contains(t, generated,
			`azd extension install "azure.ai.projects" --version "1.0.0-beta.10" --force --no-prompt`)
		assert.Less(t, strings.Index(generated, "uses: Azure/setup-azd@v2"), strings.Index(
			generated,
			`azd extension install "azure.ai.projects" --version "1.0.0-beta.10" --force --no-prompt`,
		))
	})

	t.Run("terraform layer installs terraform for github", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].PipelineDirectories[0])
		require.NoError(t, os.MkdirAll(path, osutil.PermissionDirectory))
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].Files[0])

		err := generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider:  ciProviderGitHubActions,
			IsTerraform: true,
			RepoRoot:    tempDir,
			BranchName:  "main",
			AuthType:    AuthTypeFederated,
			providerParameters: []provisioning.Parameter{
				{EnvVarMapping: []string{"AZURE_LOCATION"}},
			},
		})

		require.NoError(t, err)
		content, err := os.ReadFile(expectedPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "uses: hashicorp/setup-terraform@v3")
		assert.Equal(t, 1, strings.Count(string(content), "AZURE_LOCATION:"))
	})

	t.Run("required extensions are installed for azdo", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].PipelineDirectories[0])
		require.NoError(t, os.MkdirAll(path, osutil.PermissionDirectory))
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].Files[0])

		err := generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider: ciProviderAzureDevOps,
			RepoRoot:   tempDir,
			BranchName: "main",
			AuthType:   AuthTypeFederated,
			RequiredExtensions: []RequiredExtension{
				{Id: "azure.ai.agents", Version: "1.0.0-beta.9"},
				{Id: "azure.ai.projects", Version: "1.0.0-beta.10"},
			},
		})

		t.Run("terraform layer installs terraform for azdo", func(t *testing.T) {
			tempDir := t.TempDir()
			path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].PipelineDirectories[0])
			require.NoError(t, os.MkdirAll(path, osutil.PermissionDirectory))
			expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].Files[0])

			err := generatePipelineDefinition(expectedPath, projectProperties{
				CiProvider:  ciProviderAzureDevOps,
				IsTerraform: true,
				RepoRoot:    tempDir,
				BranchName:  "main",
				AuthType:    AuthTypeFederated,
				providerParameters: []provisioning.Parameter{
					{EnvVarMapping: []string{"AZURE_LOCATION"}},
				},
			})

			require.NoError(t, err)
			content, err := os.ReadFile(expectedPath)
			require.NoError(t, err)
			assert.Contains(t, string(content), "task: TerraformInstaller@1")
			assert.Equal(t, 2, strings.Count(string(content), "AZURE_LOCATION:"))
		})

		require.NoError(t, err)
		content, err := os.ReadFile(expectedPath)
		require.NoError(t, err)
		generated := string(content)
		assert.Contains(t, generated, "Install required azd extensions")
		assert.Contains(t, generated, "set -e")
		assert.Contains(t, generated,
			`azd extension install "azure.ai.agents" --version "1.0.0-beta.9" --force --no-prompt`)
		assert.Contains(t, generated,
			`azd extension install "azure.ai.projects" --version "1.0.0-beta.10" --force --no-prompt`)
		assert.Less(t, strings.Index(generated, "task: setup-azd@1"), strings.Index(
			generated,
			`azd extension install "azure.ai.projects" --version "1.0.0-beta.10" --force --no-prompt`,
		))
		assert.Less(t, strings.Index(generated, "curl -fsSL https://aka.ms/install-azd.sh"), strings.Index(
			generated,
			`azd extension install "azure.ai.projects" --version "1.0.0-beta.10" --force --no-prompt`,
		))
	})

	t.Run("no files - github selected - no app host - fed Cred", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   tempDir,
			HasAppHost: false,
			BranchName: "main",
			AuthType:   AuthTypeFederated,
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
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   tempDir,
			HasAppHost: true,
			BranchName: "main",
			AuthType:   AuthTypeFederated,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
	t.Run("no files - github selected - Variables and Secrets", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderGitHubActions].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   tempDir,
			HasAppHost: true,
			BranchName: "main",
			AuthType:   AuthTypeFederated,
			Variables:  []string{"VAR_1", "VAR_2"},
			Secrets:    []string{"SECRET_1", "SECRET_2"},
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
			CiProvider: ciProviderAzureDevOps,
			RepoRoot:   tempDir,
			HasAppHost: true,
			BranchName: "main",
			AuthType:   AuthTypeFederated,
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
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   tempDir,
			HasAppHost: false,
			BranchName: "main",
			AuthType:   AuthTypeClientCredentials,
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
			CiProvider: ciProviderGitHubActions,
			RepoRoot:   tempDir,
			HasAppHost: false,
			BranchName: "non-main",
			AuthType:   AuthTypeFederated,
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
			CiProvider: ciProviderAzureDevOps,
			RepoRoot:   tempDir,
			HasAppHost: false,
			BranchName: "main",
			AuthType:   AuthTypeFederated,
		})
		assert.NoError(t, err)
		// should've created the pipeline
		assert.FileExists(t, expectedPath)
		// open the file and check the content
		content, err := os.ReadFile(expectedPath)
		assert.NoError(t, err)
		snapshot.SnapshotT(t, normalizeEOL(content))
	})
	t.Run("no files - azdo selected - no app host - variables and secrets", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].PipelineDirectories[0])
		err := os.MkdirAll(path, osutil.PermissionDirectory)
		assert.NoError(t, err)
		expectedPath := filepath.Join(tempDir, pipelineProviderFiles[ciProviderAzureDevOps].Files[0])
		err = generatePipelineDefinition(expectedPath, projectProperties{
			CiProvider: ciProviderAzureDevOps,
			RepoRoot:   tempDir,
			HasAppHost: false,
			BranchName: "main",
			AuthType:   AuthTypeFederated,
			Variables:  []string{"VAR_1", "VAR_2"},
			Secrets:    []string{"SECRET_1", "SECRET_2"},
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
			CiProvider: ciProviderAzureDevOps,
			RepoRoot:   tempDir,
			HasAppHost: false,
			BranchName: "main",
			AuthType:   AuthTypeClientCredentials,
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
			CiProvider: ciProviderAzureDevOps,
			RepoRoot:   tempDir,
			HasAppHost: false,
			BranchName: "non-main",
			AuthType:   AuthTypeFederated,
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
			CiProvider: ciProviderAzureDevOps,
			RepoRoot:   tempDir,
			HasAppHost: false,
			BranchName: "main",
			AuthType:   AuthTypeFederated,
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
			CiProvider: ciProviderAzureDevOps,
			RepoRoot:   tempDir,
			HasAppHost: false,
			BranchName: "main",
			AuthType:   AuthTypeClientCredentials,
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
		project.NewImportManager(project.NewDotNetImporter(nil, nil, nil, nil, mockContext.AlphaFeaturesManager)),
		&mockUserConfigManager{},
		nil,
		armmsi.ArmMsiService{},
		&mockPrompter{},
		dotnet.NewCli(mockContext.CommandRunner),
	)
}

type mockPrompter struct{}

// recordSaves returns the .env values captured each time the pipeline manager saves its environment.
func recordSaves(pm *PipelineManager) *[]map[string]string {
	var saves []map[string]string
	pm.envManager.(*mockenv.MockEnvManager).ExpectedCalls[0].Run(func(args mock.Arguments) {
		saves = append(saves, args.Get(1).(*environment.Environment).Dotenv())
	})
	return &saves
}

// PromptLocation implements prompt.Prompter.
func (m *mockPrompter) PromptLocation(
	ctx context.Context, subId string, msg string,
	filter prompt.LocationFilterPredicate, defaultLocation *string) (string, error) {
	return "eastus", nil
}

// PromptResourceGroup implements prompt.Prompter.
func (m *mockPrompter) PromptResourceGroup(ctx context.Context, options prompt.PromptResourceOptions) (string, error) {
	return "test-rg", nil
}

// PromptResourceGroupFrom implements prompt.Prompter.
func (m *mockPrompter) PromptResourceGroupFrom(
	ctx context.Context, subscriptionId string, location string,
	options prompt.PromptResourceGroupFromOptions) (string, error) {
	return "test-rg", nil
}

// PromptSubscription implements prompt.Prompter.
func (m *mockPrompter) PromptSubscription(ctx context.Context, msg string) (subscriptionId string, err error) {
	return "00000000-0000-0000-0000-000000000000", nil
}

// IsNoPromptMode implements prompt.Prompter.
func (m *mockPrompter) IsNoPromptMode() bool {
	return false
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

func createYamlFiles(t *testing.T, tempDir string, createOptionsAndFileIndex ...any) {
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
		return strings.Contains(options.Message, "Add the default azure-dev.yml file")
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

// The app already has a password for another workload but has no service principal in this tenant.
func mockExistingPipelineApp(mc *mocks.MockContext, newSecret string) string {
	app := graphsdk.Application{
		Id: new("object-id"), AppId: new("app-id"), DisplayName: "shared-app",
		PasswordCredentials: []*graphsdk.ApplicationPasswordCredential{{KeyId: new("other-workload-key")}},
	}
	sp := graphsdk.ServicePrincipal{AppId: *app.AppId, AppOwnerOrganizationId: new("tenant-id")}
	mockgraphsdk.RegisterApplicationListMock(mc, http.StatusOK, []graphsdk.Application{app})
	mockgraphsdk.RegisterApplicationGetItemByAppIdMock(mc, http.StatusNotFound, app.DisplayName, nil)
	mockgraphsdk.RegisterApplicationGetItemByAppIdMock(mc, http.StatusOK, *app.AppId, &app)
	mockgraphsdk.RegisterServicePrincipalListMock(mc, http.StatusOK, []graphsdk.ServicePrincipal{})
	mc.HttpClient.When(func(r *http.Request) bool {
		return r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/servicePrincipals")
	}).RespondFn(func(r *http.Request) (*http.Response, error) {
		mockgraphsdk.RegisterServicePrincipalListMock(mc, http.StatusOK, []graphsdk.ServicePrincipal{sp})
		return mocks.CreateHttpResponseWithBody(r, http.StatusCreated, sp)
	})
	// Any request to remove an existing password is unexpected and fails the test.
	mockgraphsdk.RegisterApplicationAddPasswordMock(mc, http.StatusOK, *app.Id,
		&graphsdk.ApplicationPasswordCredential{KeyId: new("pipeline-key"), SecretText: &newSecret})
	return app.DisplayName
}

func TestPipelineConfig_GitHubClientCredentialsWorkflow(t *testing.T) {
	t.Setenv("AZD_GH_TOOL_PATH", "gh")
	const reminder = "azd didn't change your existing workflow"
	msiEnv := map[string]string{AzurePipelineMsiResourceId: "msi-id"}
	for _, tc := range []struct {
		name, flag       string
		env              map[string]string
		prompt           bool
		existingSp       bool
		existingWorkflow bool
		failUpload       bool
		wantReminder     bool
	}{
		{name: "interactive", prompt: true},
		{name: "explicit flag", flag: string(AuthTypeClientCredentials)},
		{
			name:             "rerun keeps saved method",
			env:              map[string]string{AzurePipelineAuthTypeEnvVarName: string(AuthTypeClientCredentials)},
			existingSp:       true,
			existingWorkflow: true,
		},
		{name: "switch from managed identity", flag: string(AuthTypeClientCredentials), env: msiEnv},
		{
			name:             "switch from managed identity with existing workflow",
			flag:             string(AuthTypeClientCredentials),
			env:              msiEnv,
			existingWorkflow: true,
			wantReminder:     true,
		},
		{
			name:       "failed upload then unflagged retry",
			flag:       string(AuthTypeClientCredentials),
			failUpload: true,
		},
		{
			name:         "failed switch then unflagged retry",
			flag:         string(AuthTypeClientCredentials),
			env:          msiEnv,
			failUpload:   true,
			wantReminder: true, // the failed run generated the workflow, so the retry sees it as existing
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			resetAzureYaml(t, filepath.Join(dir, "azure.yaml"))
			mc := resetContext(dir, t.Context())
			const newSecret = "new-test-value"
			appName := mockExistingPipelineApp(mc, newSecret)
			if tc.existingSp {
				mockgraphsdk.RegisterServicePrincipalListMock(mc, http.StatusOK, []graphsdk.ServicePrincipal{
					{AppId: "app-id", DisplayName: appName, AppOwnerOrganizationId: new("tenant-id")},
				})
			}
			if tc.existingWorkflow {
				createPipelineFiles(t, dir, ciProviderGitHubActions, 0)
			}
			mc.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
				return strings.Contains(cmd, "remote get-url origin")
			}).Respond(exec.NewRunResult(0, "https://github.com/test-owner/test-repo.git", ""))
			mc.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
				return strings.Contains(cmd, "variable set")
			}).Respond(exec.NewRunResult(0, "", ""))
			var secrets []string
			fail := tc.failUpload
			mc.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
				return strings.Contains(cmd, "secret set")
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				if fail {
					return exec.NewRunResult(1, "", "HTTP 403"), errors.New("exit status 1")
				}
				name := args.Args[len(args.Args)-1]
				secrets = append(secrets, name)
				if name == "ARM_CLIENT_SECRET" {
					value, err := io.ReadAll(args.StdIn)
					require.NoError(t, err)
					require.Equal(t, newSecret, string(value))
				}
				return exec.NewRunResult(0, "", ""), nil
			})
			if tc.prompt {
				mc.Console.WhenSelect(func(o input.ConsoleOptions) bool {
					return slices.Contains(o.Options, "Client Credentials (SP + Secret)")
				}).RespondFn(func(o input.ConsoleOptions) (any, error) {
					return slices.Index(o.Options, "Client Credentials (SP + Secret)"), nil
				})
			}
			mc.Console.WhenConfirm(func(o input.ConsoleOptions) bool {
				return o.Message == "Add the default azure-dev.yml file?"
			}).Respond(true)
			mc.Console.WhenConfirm(func(o input.ConsoleOptions) bool {
				return strings.Contains(o.Message, "Commit and push")
			}).Respond(false)
			env := environment.NewWithValues("test", map[string]string{
				AzurePipelineClientIdEnvVarName: "",
				AzurePipelineMsiResourceId:      "",
				"RS_RESOURCE_GROUP":             "rg",
				"RS_STORAGE_ACCOUNT":            "storage",
				"RS_CONTAINER_NAME":             "state",
			})
			for k, v := range tc.env {
				env.DotenvSet(k, v)
			}
			pm, err := createPipelineManager(mc, azdcontext.NewAzdContextWithDirectory(dir), env, &PipelineManagerArgs{
				PipelineServicePrincipalName: appName,
				PipelineProvider:             gitHubCode,
				PipelineRemoteName:           "origin",
				PipelineAuthTypeName:         tc.flag,
			})
			require.NoError(t, err)
			saves := recordSaves(pm)
			_, err = pm.Configure(t.Context(), "test", &project.Infra{
				Options: provisioning.Options{Provider: provisioning.Terraform},
			})
			if tc.failUpload {
				require.Error(t, err)
				// The last save before the failed upload must already hold the client ID and requested method.
				require.NotEmpty(t, *saves)
				saved := (*saves)[len(*saves)-1]
				assert.Equal(t, "app-id", saved[AzurePipelineClientIdEnvVarName])
				assert.Equal(t, string(AuthTypeClientCredentials), saved[AzurePipelineAuthTypeEnvVarName])
				assert.Equal(t, tc.env[AzurePipelineMsiResourceId], saved[AzurePipelineMsiResourceId])
				fail = false
				pm.args.PipelineAuthTypeName = ""
				_, err = pm.Configure(t.Context(), "test", &project.Infra{
					Options: provisioning.Options{Provider: provisioning.Terraform},
				})
			}
			require.NoError(t, err)
			require.ElementsMatch(t, []string{"AZURE_CREDENTIALS", "ARM_CLIENT_SECRET"}, secrets)
			assert.Equal(t, string(AuthTypeClientCredentials), env.Getenv(AzurePipelineAuthTypeEnvVarName))
			assert.Empty(t, env.Getenv(AzurePipelineMsiResourceId))
			out := strings.Join(mc.Console.Output(), "\n")
			assert.Contains(t, out, "https://github.com/test-owner/test-repo/settings/secrets/actions")
			assert.Equal(t, tc.wantReminder, strings.Contains(out, reminder))
			if tc.existingWorkflow {
				return
			}
			yaml, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "azure-dev.yml"))
			require.NoError(t, err)
			assert.Contains(t, string(yaml), "AZURE_CREDENTIALS: ${{ secrets.AZURE_CREDENTIALS }}")
			assert.Contains(t, string(yaml), "ARM_CLIENT_SECRET: ${{ secrets.ARM_CLIENT_SECRET }}")
			assert.NotContains(t, string(yaml), "--federated-credential-provider")
		})
	}
}

// A failed managed identity setup in an environment that previously used client credentials must not leave the
// managed identity next to a saved client-credentials method, which a retry would treat as an unfinished switch.
func TestPipelineConfig_FailedMsiSetupClearsSavedMethod(t *testing.T) {
	t.Setenv("AZD_GH_TOOL_PATH", "gh")
	dir := t.TempDir()
	resetAzureYaml(t, filepath.Join(dir, "azure.yaml"))
	mc := resetContext(dir, t.Context())
	const msiId = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/msi"
	mc.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
		return strings.Contains(cmd, "remote get-url origin")
	}).Respond(exec.NewRunResult(0, "https://github.com/test-owner/test-repo.git", ""))
	mc.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
		return strings.Contains(cmd, "/actions/oidc/customization/sub")
	}).Respond(exec.NewRunResult(1, "", "HTTP 500"))
	mc.HttpClient.When(func(r *http.Request) bool {
		return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/userAssignedIdentities")
	}).RespondFn(func(r *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(r, http.StatusOK, map[string]any{"value": []msi.Identity{{
			ID:   new(msiId),
			Name: new("msi"),
			Properties: &msi.UserAssignedIdentityProperties{
				PrincipalID: new("principal-id"), ClientID: new("client-id"), TenantID: new("tenant-id"),
			},
		}}})
	})
	mc.Console.WhenSelect(func(o input.ConsoleOptions) bool {
		return o.Message == "Select how to authenticate the pipeline to Azure:"
	}).Respond(0)
	mc.Console.WhenSelect(func(o input.ConsoleOptions) bool {
		return strings.HasPrefix(o.Message, "Create a new User Managed Identity")
	}).Respond(1)
	mc.Console.WhenSelect(func(o input.ConsoleOptions) bool {
		return strings.HasPrefix(o.Message, "Select an existing User Managed Identity")
	}).Respond(0)
	mc.Console.WhenConfirm(func(o input.ConsoleOptions) bool {
		return o.Message == "Add the default azure-dev.yml file?"
	}).Respond(true)
	env := environment.NewWithValues("test", map[string]string{
		AzurePipelineAuthTypeEnvVarName:      string(AuthTypeClientCredentials),
		environment.SubscriptionIdEnvVarName: "sub",
	})
	pm, err := createPipelineManager(mc, azdcontext.NewAzdContextWithDirectory(dir), env, &PipelineManagerArgs{
		PipelineProvider: gitHubCode, PipelineRemoteName: "origin",
	})
	require.NoError(t, err)
	pm.msiService = armmsi.NewArmMsiService(mc.SubscriptionCredentialProvider, mc.ArmClientOptions)

	_, err = pm.Configure(t.Context(), "test", &project.Infra{})
	require.ErrorContains(t, err, "OIDC")
	assert.Equal(t, msiId, env.Getenv(AzurePipelineMsiResourceId))
	_, hasAuthType := env.LookupDotenv(AzurePipelineAuthTypeEnvVarName)
	assert.False(t, hasAuthType)
}

func TestPipelineConfig_NestedProjectWithUnpublishedWorkflow(t *testing.T) {
	t.Setenv("AZD_GH_TOOL_PATH", "gh")
	root := t.TempDir()
	projectDir := filepath.Join(root, "app")
	require.NoError(t, os.Mkdir(projectDir, osutil.PermissionDirectory))
	resetAzureYaml(t, filepath.Join(projectDir, "azure.yaml"))

	const repoURL = "https://github.com/test-owner/test-repo"
	mc := resetContext(root, t.Context())
	mc.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
		return strings.Contains(cmd, "remote get-url origin")
	}).Respond(exec.NewRunResult(0, repoURL+".git", ""))
	mc.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
		return strings.HasSuffix(cmd, "/repos/test-owner/test-repo/actions/workflows")
	}).Respond(exec.NewRunResult(0, `{"total_count":1,"workflows":[{"path":".github/workflows/other.yml"}]}`, ""))
	mc.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Equal(args.Args, []string{"-C", projectDir, "add", "."}) || slices.Contains(args.Args, "commit")
	}).Respond(exec.NewRunResult(0, "", ""))
	var pushed, checkedWorkflow bool
	mc.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "push")
	}).RespondFn(func(exec.RunArgs) (exec.RunResult, error) {
		pushed = true
		return exec.NewRunResult(0, "", ""), nil
	})
	mc.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
		return strings.HasSuffix(cmd, "/repos/test-owner/test-repo/actions/workflows/azure-dev.yml")
	}).RespondFn(func(exec.RunArgs) (exec.RunResult, error) {
		require.True(t, pushed, "the lookup must follow the push")
		checkedWorkflow = true
		return exec.NewRunResult(1, "", "gh: Not Found (HTTP 404)"), fmt.Errorf("exit status 1")
	})
	mc.Console.WhenSelect(func(o input.ConsoleOptions) bool {
		return slices.Contains(o.Options, "Skip authentication setup")
	}).RespondFn(func(o input.ConsoleOptions) (any, error) {
		return slices.Index(o.Options, "Skip authentication setup"), nil
	})
	mc.Console.WhenConfirm(func(o input.ConsoleOptions) bool {
		return o.Message == "Add the default azure-dev.yml file?" || strings.Contains(o.Message, "Commit and push")
	}).Respond(true)
	env := environment.NewWithValues("test", map[string]string{
		AzurePipelineClientIdEnvVarName: "",
		AzurePipelineMsiResourceId:      "",
	})
	pm, err := createPipelineManager(mc, azdcontext.NewAzdContextWithDirectory(projectDir), env, &PipelineManagerArgs{
		PipelineProvider: gitHubCode, PipelineRemoteName: "origin",
	})
	require.NoError(t, err)
	result, err := pm.Configure(t.Context(), "test", &project.Infra{})
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, ".github", "workflows", "azure-dev.yml"))
	assert.True(t, checkedWorkflow)
	assert.Equal(t, repoURL+"/actions", result.PipelineLink)
}

func Test_PipelineManager_resolvePipelineAuth(t *testing.T) {
	tests := []struct {
		name                 string
		authTypeArg          string
		ciProvider           ciProviderType
		savedAuthType        string
		usingMsi             bool
		usingAppRegistration bool
		selectOption         string
		wantPrompted         bool
		wantOptionCount      int
		want                 pipelineAuthSelection
		wantErr              string
	}{
		{
			name:            "prompts and selects MSI by default",
			selectOption:    "Federated Managed Identity (MSI + OIDC) (Recommended)",
			wantPrompted:    true,
			wantOptionCount: 4,
			want: pipelineAuthSelection{
				usingMsi: true,
				authType: AuthTypeFederated,
			},
		},
		{
			name:            "prompts and selects federated service principal",
			selectOption:    "Federated Service Principal (SP + OIDC)",
			wantPrompted:    true,
			wantOptionCount: 4,
			want: pipelineAuthSelection{
				usingAppRegistration: true,
				authType:             AuthTypeFederated,
			},
		},
		{
			name:            "prompts and selects client credentials",
			selectOption:    "Client Credentials (SP + Secret)",
			wantPrompted:    true,
			wantOptionCount: 4,
			want: pipelineAuthSelection{
				usingAppRegistration: true,
				authType:             AuthTypeClientCredentials,
			},
		},
		{
			name:            "prompts and selects skip",
			selectOption:    "Skip authentication setup",
			wantPrompted:    true,
			wantOptionCount: 4,
			want: pipelineAuthSelection{
				skipAuth: true,
			},
		},
		{
			name:        "explicit client-credentials skips prompt and uses service principal",
			authTypeArg: string(AuthTypeClientCredentials),
			want: pipelineAuthSelection{
				usingAppRegistration: true,
				authType:             AuthTypeClientCredentials,
			},
		},
		{
			name:                 "existing service principal keeps saved client-credentials",
			savedAuthType:        string(AuthTypeClientCredentials),
			usingAppRegistration: true,
			want: pipelineAuthSelection{
				usingAppRegistration: true,
				authType:             AuthTypeClientCredentials,
			},
		},
		{
			name:                 "explicit auth type overrides saved method",
			authTypeArg:          string(AuthTypeFederated),
			savedAuthType:        string(AuthTypeClientCredentials),
			usingAppRegistration: true,
			want: pipelineAuthSelection{
				usingAppRegistration: true,
				authType:             AuthTypeFederated,
			},
		},
		{
			name:                 "invalid saved auth type returns error",
			savedAuthType:        "secret",
			usingAppRegistration: true,
			wantErr:              `invalid AZURE_PIPELINE_AUTH_TYPE value "secret"`,
		},
		{
			name:            "explicit federated omits client credentials option",
			authTypeArg:     string(AuthTypeFederated),
			selectOption:    "Federated Service Principal (SP + OIDC)",
			wantPrompted:    true,
			wantOptionCount: 3,
			want: pipelineAuthSelection{
				usingAppRegistration: true,
				authType:             AuthTypeFederated,
			},
		},
		{
			name:                 "existing service principal skips prompt on GitHub",
			ciProvider:           ciProviderGitHubActions,
			usingAppRegistration: true,
			want: pipelineAuthSelection{
				usingAppRegistration: true,
			},
		},
		{
			name:                 "legacy Azure DevOps service principal prompts for client credentials",
			ciProvider:           ciProviderAzureDevOps,
			usingAppRegistration: true,
			selectOption:         "Client Credentials (SP + Secret)",
			wantPrompted:         true,
			wantOptionCount:      2,
			want: pipelineAuthSelection{
				usingAppRegistration: true,
				authType:             AuthTypeClientCredentials,
			},
		},
		{
			name:                 "legacy Azure DevOps service principal defaults to federated",
			ciProvider:           ciProviderAzureDevOps,
			usingAppRegistration: true,
			wantPrompted:         true,
			wantOptionCount:      2,
			want: pipelineAuthSelection{
				usingAppRegistration: true,
				authType:             AuthTypeFederated,
			},
		},
		{
			name:                 "saved Azure DevOps method skips prompt",
			ciProvider:           ciProviderAzureDevOps,
			savedAuthType:        string(AuthTypeClientCredentials),
			usingAppRegistration: true,
			want: pipelineAuthSelection{
				usingAppRegistration: true,
				authType:             AuthTypeClientCredentials,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			prompted := false
			mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
				return options.Message == "Select how to authenticate the pipeline to Azure:" ||
					options.Message == "Select the authentication method for this Azure DevOps pipeline:"
			}).RespondFn(func(options input.ConsoleOptions) (any, error) {
				prompted = true
				require.True(t, options.ShowOptionDetailsBelow)
				require.Len(t, options.Options, tt.wantOptionCount)
				require.Len(t, options.OptionDetails, tt.wantOptionCount)
				selectOption := cmp.Or(tt.selectOption, options.DefaultValue.(string))
				for i, opt := range options.Options {
					if opt == selectOption {
						return i, nil
					}
				}
				return 0, fmt.Errorf("option %q not found in %v", selectOption, options.Options)
			})

			pm := &PipelineManager{
				args:           &PipelineManagerArgs{PipelineAuthTypeName: tt.authTypeArg},
				console:        mockContext.Console,
				ciProviderType: tt.ciProvider,
				env: environment.NewWithValues("dev", map[string]string{
					AzurePipelineAuthTypeEnvVarName: tt.savedAuthType,
				}),
			}

			got, err := pm.resolvePipelineAuth(t.Context(), tt.usingMsi, tt.usingAppRegistration)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				_, ok := errors.AsType[*errorhandler.ErrorWithSuggestion](err)
				require.True(t, ok)
				require.False(t, prompted)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantPrompted, prompted)
			require.Equal(t, tt.want, got)
		})
	}
}
