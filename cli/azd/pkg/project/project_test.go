// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/pkg/azapi"
	"github.com/azure/azure-dev/pkg/azure"
	"github.com/azure/azure-dev/pkg/environment"
	"github.com/azure/azure-dev/pkg/ext"
	"github.com/azure/azure-dev/pkg/infra"
	"github.com/azure/azure-dev/pkg/infra/provisioning"
	"github.com/azure/azure-dev/pkg/osutil"
	"github.com/azure/azure-dev/test/mocks"
	"github.com/azure/azure-dev/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/test/mocks/mockazapi"
	"github.com/azure/azure-dev/test/snapshot"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Specifying resource name in the project file should override the default
func TestResourceNameOverrideFromProjectFile(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  api:
    resourceName: deployedApiSvc
    project: src/api
    language: js
    host: appservice
`
	mockContext := mocks.NewMockContext(context.Background())
	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		to.Ptr("rg-test"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       to.Ptr("deployedApiSvc"),
				Name:     to.Ptr("deployedApiSvc"),
				Type:     to.Ptr(string(azapi.AzureResourceTypeWebSite)),
				Location: to.Ptr("eastus2"),
			},
		})
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	azureResourceManager := infra.NewAzureResourceManager(resourceService, deploymentService)
	resourceManager := NewResourceManager(env, deploymentService, resourceService, azureResourceManager)
	targetResource, err := resourceManager.GetTargetResource(
		*mockContext.Context, env.GetSubscriptionId(), projectConfig.Services["api"])
	require.NoError(t, err)
	require.NotNil(t, targetResource)

	require.Equal(t, "deployedApiSvc", targetResource.ResourceName())
}

func TestResourceNameOverrideFromResourceTag(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  api:
    project: src/api
    language: js
    host: appservice
`
	mockContext := mocks.NewMockContext(context.Background())
	resourceName := "app-api-abc123"
	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		to.Ptr("rg-test"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       to.Ptr("app-api-abc123"),
				Name:     &resourceName,
				Type:     to.Ptr(string(azapi.AzureResourceTypeWebSite)),
				Location: to.Ptr("eastus2"),
				Tags: map[string]*string{
					azure.TagKeyAzdServiceName: to.Ptr("api"),
				},
			},
		},
	)
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	azureResourceManager := infra.NewAzureResourceManager(resourceService, deploymentService)
	resourceManager := NewResourceManager(env, deploymentService, resourceService, azureResourceManager)
	targetResource, err := resourceManager.GetTargetResource(
		*mockContext.Context, env.GetSubscriptionId(), projectConfig.Services["api"])
	require.NoError(t, err)
	require.NotNil(t, targetResource)
	require.Equal(t, resourceName, targetResource.ResourceName())
}

func TestResourceGroupOverrideFromProjectFile(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-custom-group
services:
  web:
    project: src/web
    language: js
    host: appservice
  api:
    resourceName: deployedApiSvc
    project: src/api
    language: js
    host: appservice
`
	mockContext := mocks.NewMockContext(context.Background())
	resourceGroupName := "rg-custom-group"
	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		&resourceGroupName,
		[]*armresources.GenericResourceExpanded{
			{
				ID:       to.Ptr("deployedApiSvc"),
				Name:     to.Ptr("deployedApiSvc"),
				Type:     to.Ptr(string(azapi.AzureResourceTypeWebSite)),
				Location: to.Ptr("eastus2"),
			},
			{
				ID:       to.Ptr("webResource"),
				Name:     to.Ptr("webResource"),
				Type:     to.Ptr(string(azapi.AzureResourceTypeWebSite)),
				Location: to.Ptr("eastus2"),
				Tags: map[string]*string{
					azure.TagKeyAzdServiceName: to.Ptr("web"),
				},
			},
		})
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	azureResourceManager := infra.NewAzureResourceManager(resourceService, deploymentService)
	resourceManager := NewResourceManager(env, deploymentService, resourceService, azureResourceManager)

	for _, svc := range projectConfig.Services {
		targetResource, err := resourceManager.GetTargetResource(*mockContext.Context, env.GetSubscriptionId(), svc)
		require.NoError(t, err)
		require.NotNil(t, targetResource)
		require.Equal(t, resourceGroupName, targetResource.ResourceGroupName())
	}
}

func TestResourceGroupOverrideFromEnv(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
services:
  web:
    project: src/web
    language: js
    host: appservice
  api:
    resourceName: deployedApiSvc
    project: src/api
    language: js
    host: appservice
`
	mockContext := mocks.NewMockContext(context.Background())

	expectedResourceGroupName := "custom-name-from-env-rg"

	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		&expectedResourceGroupName,
		[]*armresources.GenericResourceExpanded{
			{
				ID:       to.Ptr("deployedApiSvc"),
				Name:     to.Ptr("deployedApiSvc"),
				Type:     to.Ptr(string(azapi.AzureResourceTypeWebSite)),
				Location: to.Ptr("eastus2"),
			},
			{
				ID:       to.Ptr("webResource"),
				Name:     to.Ptr("webResource"),
				Type:     to.Ptr(string(azapi.AzureResourceTypeWebSite)),
				Location: to.Ptr("eastus2"),
				Tags: map[string]*string{
					azure.TagKeyAzdServiceName: to.Ptr("web"),
				},
			},
		})
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.ResourceGroupEnvVarName:  expectedResourceGroupName,
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	azureResourceManager := infra.NewAzureResourceManager(resourceService, deploymentService)
	resourceManager := NewResourceManager(env, deploymentService, resourceService, azureResourceManager)
	targetResource, err := resourceManager.GetTargetResource(
		*mockContext.Context, env.GetSubscriptionId(), projectConfig.Services["api"])
	require.NoError(t, err)
	require.NotNil(t, targetResource)

	for _, svc := range projectConfig.Services {
		targetResource, err := resourceManager.GetTargetResource(*mockContext.Context, env.GetSubscriptionId(), svc)
		require.NoError(t, err)
		require.NotNil(t, targetResource)
		require.Equal(t, expectedResourceGroupName, targetResource.ResourceGroupName())
	}
}

func Test_Invalid_Project_File(t *testing.T) {
	tests := map[string]string{
		"Empty":      "",
		"Spaces":     "  ",
		"Lines":      "\n\n\n",
		"Tabs":       "\t\t\t",
		"Whitespace": " \t \n \t \n \t \n",
		"InvalidYaml": `
			name: test-proj
			metadata:
				template: test-proj-template
			services:
		`,
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			projectConfig, err := Parse(context.Background(), test)
			require.Nil(t, projectConfig)
			require.Error(t, err)
		})
	}
}

func TestMinimalYaml(t *testing.T) {
	prj := &ProjectConfig{
		Name:     "minimal",
		Services: map[string]*ServiceConfig{},
	}

	t.Run("project only", func(t *testing.T) {
		contents, err := yaml.Marshal(prj)
		require.NoError(t, err)

		snapshot.SnapshotT(t, string(contents))
	})

	tests := []struct {
		name          string
		serviceConfig ServiceConfig
	}{
		{
			"minimal-service",
			ServiceConfig{
				Name:         "ignored",
				Language:     ServiceLanguagePython,
				Host:         AppServiceTarget,
				RelativePath: "./src",
			},
		},
		{
			"minimal-docker",
			ServiceConfig{
				Name:     "ignored",
				Language: ServiceLanguageDotNet,
				Host:     ContainerAppTarget,
				Docker: DockerProjectOptions{
					Path: "./Dockerfile",
				},
				RelativePath: "./src",
			},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prj.Services = map[string]*ServiceConfig{
				tt.name: &tests[i].serviceConfig,
			}

			contents, err := yaml.Marshal(prj)
			require.NoError(t, err)

			snapshot.SnapshotT(t, string(contents))
		})
	}
}

// Test_WindowsStylePathsFromYaml ensures that paths using a backslash are a seperator are correctly parsed from yaml.
// `azd` prefers forward slashes as path separators, to allow for consistent handling across platforms, but supports
// backslashes in yaml files, and treats them as if the user had used forward slashes instead.
func Test_WindowsStylePathsFromYaml(t *testing.T) {
	const testProj = `
name: test-proj
infra:
  path: .\iac
services:
  api:
    host: containerapp
    language: js
    project: src\api
    dist: bin\api
`

	projectConfig, err := Parse(context.Background(), testProj)
	require.NoError(t, err)

	assert.Equal(t, filepath.FromSlash("./iac"), projectConfig.Infra.Path)
	assert.Equal(t, filepath.FromSlash("src/api"), projectConfig.Services["api"].RelativePath)
	assert.Equal(t, filepath.FromSlash("bin/api"), projectConfig.Services["api"].OutputPath)
}

func Test_HooksFromFolderPath(t *testing.T) {
	t.Run("ProjectInfraHooks", func(t *testing.T) {
		prj := &ProjectConfig{
			Name:     "minimal",
			Services: map[string]*ServiceConfig{},
		}
		contents, err := yaml.Marshal(prj)
		require.NoError(t, err)

		tempDir := t.TempDir()

		azureYamlPath := filepath.Join(tempDir, "azure.yaml")
		err = os.WriteFile(azureYamlPath, contents, osutil.PermissionDirectory)
		require.NoError(t, err)

		infraPath := filepath.Join(tempDir, "infra")
		err = os.Mkdir(infraPath, osutil.PermissionDirectory)
		require.NoError(t, err)

		hooksPath := filepath.Join(infraPath, "main.hooks.yaml")
		hooksContent := []byte(`
prebuild:
  shell: sh
  run: ./pre-build.sh
postbuild:
  shell: pwsh
  run: ./post-build.ps1
`)

		err = os.WriteFile(hooksPath, hooksContent, osutil.PermissionDirectory)
		require.NoError(t, err)

		expectedHooks := HooksConfig{
			"prebuild": {{
				Name:            "",
				Shell:           ext.ShellTypeBash,
				Run:             "./pre-build.sh",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			}},
			"postbuild": {{
				Name:            "",
				Shell:           ext.ShellTypePowershell,
				Run:             "./post-build.ps1",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			},
			}}

		project, err := Load(context.Background(), azureYamlPath)
		require.NoError(t, err)
		require.Equal(t, expectedHooks, project.Hooks)
	})

	t.Run("DoubleDefintionHooks", func(t *testing.T) {
		prj := &ProjectConfig{
			Name:     "minimal",
			Services: map[string]*ServiceConfig{},
			Hooks: HooksConfig{
				"prebuild": {{
					Shell: ext.ShellTypeBash,
					Run:   "./pre-build.sh",
				}},
			},
		}
		contents, err := yaml.Marshal(prj)
		require.NoError(t, err)

		tempDir := t.TempDir()

		azureYamlPath := filepath.Join(tempDir, "azure.yaml")
		err = os.WriteFile(azureYamlPath, contents, osutil.PermissionDirectory)
		require.NoError(t, err)

		infraPath := filepath.Join(tempDir, "infra")
		err = os.Mkdir(infraPath, osutil.PermissionDirectory)
		require.NoError(t, err)

		hooksPath := filepath.Join(infraPath, "main.hooks.yaml")
		hooksContent := []byte(`
prebuild:
  shell: sh
  run: ./pre-build-external.sh
postbuild:
  shell: pwsh
  run: ./post-build.ps1
`)

		err = os.WriteFile(hooksPath, hooksContent, osutil.PermissionDirectory)
		require.NoError(t, err)

		project, err := Load(context.Background(), azureYamlPath)
		require.NoError(t, err)
		expectedHooks := HooksConfig{
			"prebuild": {{
				Name:            "",
				Shell:           ext.ShellTypeBash,
				Run:             "./pre-build.sh",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			}, {
				Name:            "",
				Shell:           ext.ShellTypeBash,
				Run:             "./pre-build-external.sh",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			}},
			"postbuild": {{
				Name:            "",
				Shell:           ext.ShellTypePowershell,
				Run:             "./post-build.ps1",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			}},
		}
		require.Equal(t, expectedHooks, project.Hooks)
	})
}

func TestInfraDefaultsNotSavedToYaml(t *testing.T) {
	t.Run("DefaultValuesNotWritten", func(t *testing.T) {
		// Create a minimal project config with no infra settings
		projectConfig := &ProjectConfig{
			Name: "test-project",
			// Infra field left empty - should use defaults at runtime but not save them
		}

		// Create a temporary file for testing
		tempDir := t.TempDir()
		projectFile := filepath.Join(tempDir, "azure.yaml")

		// Save the project - should not include default infra values
		err := Save(context.Background(), projectConfig, projectFile)
		require.NoError(t, err)

		// Read the file content directly to verify defaults aren't written
		fileContent, err := os.ReadFile(projectFile)
		require.NoError(t, err)

		yamlContent := string(fileContent)

		// Verify that default values are NOT present in the saved YAML
		assert.NotContains(t, yamlContent, "infra:")
		assert.NotContains(t, yamlContent, "path: infra")
		assert.NotContains(t, yamlContent, "module: main")
		assert.NotContains(t, yamlContent, "provider: bicep")

		// Load the project back - should work with defaults applied at runtime
		loadedProject, err := Load(context.Background(), projectFile)
		require.NoError(t, err)

		// Verify the project loaded correctly with the right name
		assert.Equal(t, "test-project", loadedProject.Name)

		// The loaded project's Infra field should still be empty/default
		// (defaults are only applied when needed, not stored in the config)
		assert.Equal(t, "", loadedProject.Infra.Path)
		assert.Equal(t, "", loadedProject.Infra.Module)
		assert.Equal(t, provisioning.ProviderKind(""), loadedProject.Infra.Provider)
	})

	t.Run("CustomValuesAreWritten", func(t *testing.T) {
		// Create a project config with custom infra settings
		projectConfig := &ProjectConfig{
			Name: "test-project",
			Infra: provisioning.Options{
				Path:     "custom-infra",
				Module:   "custom-main",
				Provider: provisioning.Terraform,
			},
		}

		// Create a temporary file for testing
		tempDir := t.TempDir()
		projectFile := filepath.Join(tempDir, "azure.yaml")

		// Save the project - should include custom infra values
		err := Save(context.Background(), projectConfig, projectFile)
		require.NoError(t, err)

		// Read the file content directly to verify custom values are written
		fileContent, err := os.ReadFile(projectFile)
		require.NoError(t, err)

		yamlContent := string(fileContent)

		// Verify that custom values ARE present in the saved YAML
		assert.Contains(t, yamlContent, "infra:")
		assert.Contains(t, yamlContent, "path: custom-infra")
		assert.Contains(t, yamlContent, "module: custom-main")
		assert.Contains(t, yamlContent, "provider: terraform")

		// Load the project back
		loadedProject, err := Load(context.Background(), projectFile)
		require.NoError(t, err)

		// Verify the custom values are preserved
		assert.Equal(t, "test-project", loadedProject.Name)
		assert.Equal(t, "custom-infra", loadedProject.Infra.Path)
		assert.Equal(t, "custom-main", loadedProject.Infra.Module)
		assert.Equal(t, provisioning.Terraform, loadedProject.Infra.Provider)
	})

	t.Run("PartialCustomValuesWritten", func(t *testing.T) {
		// Create a project config with only some custom infra settings
		projectConfig := &ProjectConfig{
			Name: "test-project",
			Infra: provisioning.Options{
				Path:     "my-infra", // Only path and provider are custom, module should use default
				Provider: provisioning.Terraform,
				// Module left empty - should use default at runtime
			},
		}

		// Create a temporary file for testing
		tempDir := t.TempDir()
		projectFile := filepath.Join(tempDir, "azure.yaml")

		// Save the project
		err := Save(context.Background(), projectConfig, projectFile)
		require.NoError(t, err)

		// Read the file content
		fileContent, err := os.ReadFile(projectFile)
		require.NoError(t, err)

		yamlContent := string(fileContent)

		// Verify only the custom values are written, not the default module
		assert.Contains(t, yamlContent, "infra:")
		assert.Contains(t, yamlContent, "path: my-infra")
		assert.Contains(t, yamlContent, "provider: terraform")
		assert.NotContains(t, yamlContent, "module: main") // Default not written

		// Load the project back
		loadedProject, err := Load(context.Background(), projectFile)
		require.NoError(t, err)

		// Verify the custom values are preserved and module is empty (will use default at runtime)
		assert.Equal(t, "my-infra", loadedProject.Infra.Path)
		assert.Equal(t, provisioning.Terraform, loadedProject.Infra.Provider)
		assert.Equal(t, "", loadedProject.Infra.Module) // Empty in config, but defaults applied when needed
	})

	t.Run("OnlyProviderCustomValue", func(t *testing.T) {
		// Create a project config with only provider set to non-default
		projectConfig := &ProjectConfig{
			Name: "test-project",
			Infra: provisioning.Options{
				Provider: provisioning.Terraform,
				// Path and Module left empty - should use defaults at runtime
			},
		}

		// Create a temporary file for testing
		tempDir := t.TempDir()
		projectFile := filepath.Join(tempDir, "azure.yaml")

		// Save the project
		err := Save(context.Background(), projectConfig, projectFile)
		require.NoError(t, err)

		// Read the file content
		fileContent, err := os.ReadFile(projectFile)
		require.NoError(t, err)

		yamlContent := string(fileContent)

		// Verify only the custom provider is written, not the default path/module
		assert.Contains(t, yamlContent, "infra:")
		assert.Contains(t, yamlContent, "provider: terraform")
		assert.NotContains(t, yamlContent, "path: infra")  // Default not written
		assert.NotContains(t, yamlContent, "module: main") // Default not written

		// Load the project back
		loadedProject, err := Load(context.Background(), projectFile)
		require.NoError(t, err)

		// Verify the custom provider is preserved and path/module are empty (will use defaults at runtime)
		assert.Equal(t, provisioning.Terraform, loadedProject.Infra.Provider)
		assert.Equal(t, "", loadedProject.Infra.Path)   // Empty in config, but defaults applied when needed
		assert.Equal(t, "", loadedProject.Infra.Module) // Empty in config, but defaults applied when needed
	})

	t.Run("LayersWithDefaultValues", func(t *testing.T) {
		// Create a project config with layers but default infra values
		projectConfig := &ProjectConfig{
			Name: "test-project",
			Infra: provisioning.Options{
				Layers: []provisioning.Options{
					{
						Name:   "networking",
						Path:   "infra/networking", // Custom path
						Module: "network",
						// Provider left to default
					},
					{
						Name:     "application",
						Path:     "infra/app", // Custom path
						Module:   "app",
						Provider: provisioning.Terraform, // Custom provider for this layer
					},
				},
				// Root infra settings left to defaults
			},
		}

		// Create a temporary file for testing
		tempDir := t.TempDir()
		projectFile := filepath.Join(tempDir, "azure.yaml")

		// Save the project
		err := Save(context.Background(), projectConfig, projectFile)
		require.NoError(t, err)

		// Read the file content
		fileContent, err := os.ReadFile(projectFile)
		require.NoError(t, err)

		yamlContent := string(fileContent)

		// Verify that layers are written but default root infra values are not
		assert.Contains(t, yamlContent, "infra:")
		assert.Contains(t, yamlContent, "layers:")
		assert.Contains(t, yamlContent, "name: networking")
		assert.Contains(t, yamlContent, "path: infra/networking")
		assert.Contains(t, yamlContent, "module: network")
		assert.Contains(t, yamlContent, "name: application")
		assert.Contains(t, yamlContent, "path: infra/app")
		assert.Contains(t, yamlContent, "module: app")
		assert.Contains(t, yamlContent, "provider: terraform") // Custom provider for layer

		// Verify default root infra values are NOT written (check for root-level defaults)
		// Since layers contain their own path/module/provider, we need to check that root defaults aren't present
		// We can't use simple string contains because layers have their own paths, so check structure
		assert.NotRegexp(t, `(?m)^path: infra$`, yamlContent)     // Root default path not written
		assert.NotRegexp(t, `(?m)^module: main$`, yamlContent)    // Root default module not written
		assert.NotRegexp(t, `(?m)^provider: bicep$`, yamlContent) // Root default provider not written

		// Load the project back
		loadedProject, err := Load(context.Background(), projectFile)
		require.NoError(t, err)

		// Verify the layers are preserved
		require.Len(t, loadedProject.Infra.Layers, 2)
		assert.Equal(t, "networking", loadedProject.Infra.Layers[0].Name)
		assert.Equal(t, "infra/networking", loadedProject.Infra.Layers[0].Path)
		assert.Equal(t, "network", loadedProject.Infra.Layers[0].Module)
		assert.Equal(t, provisioning.ProviderKind(""), loadedProject.Infra.Layers[0].Provider) // Default not stored (empty)

		assert.Equal(t, "application", loadedProject.Infra.Layers[1].Name)
		assert.Equal(t, "infra/app", loadedProject.Infra.Layers[1].Path)
		assert.Equal(t, "app", loadedProject.Infra.Layers[1].Module)
		assert.Equal(t, provisioning.Terraform, loadedProject.Infra.Layers[1].Provider) // Custom value preserved

		// Verify root infra values are empty (will use defaults at runtime)
		assert.Equal(t, "", loadedProject.Infra.Path)
		assert.Equal(t, "", loadedProject.Infra.Module)
		assert.Equal(t, provisioning.ProviderKind(""), loadedProject.Infra.Provider)
	})
}
