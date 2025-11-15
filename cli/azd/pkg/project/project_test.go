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
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
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

func TestAdditionalPropertiesMarshalling(t *testing.T) {
	tests := []struct {
		name    string
		project *ProjectConfig
	}{
		{
			"project-level-extensions",
			&ProjectConfig{
				Name: "test-extension-project",
				Services: map[string]*ServiceConfig{
					"api": {
						Language:     ServiceLanguageJavaScript,
						Host:         ContainerAppTarget,
						RelativePath: "./src/api",
					},
				},
				AdditionalProperties: map[string]interface{}{
					"customProjectField": "project-level-extension",
					"organizationSettings": map[string]interface{}{
						"billing":    "department-a",
						"compliance": true,
						"tags":       []interface{}{"production", "critical"},
					},
					"extensionConfig": map[string]interface{}{
						"timeout": 300,
						"retries": 3,
						"database": map[string]interface{}{
							"host": "localhost",
							"port": 5432,
						},
					},
				},
			},
		},
		{
			"service-level-extensions",
			&ProjectConfig{
				Name: "test-service-extension",
				Services: map[string]*ServiceConfig{
					"api": {
						Language:     ServiceLanguageJavaScript,
						Host:         ContainerAppTarget,
						RelativePath: "./src/api",
						AdditionalProperties: map[string]interface{}{
							"customServiceField": "service-level-extension",
							"monitoring": map[string]interface{}{
								"metrics": true,
								"logging": "verbose",
								"alerts":  []interface{}{"cpu > 80%", "memory > 90%"},
							},
							"extensionSettings": map[string]interface{}{
								"caching": "redis",
								"timeout": 30,
								"features": map[string]interface{}{
									"featureA": true,
									"featureB": false,
								},
							},
						},
					},
					"web": {
						Language:     ServiceLanguageTypeScript,
						Host:         StaticWebAppTarget,
						RelativePath: "./src/web",
						AdditionalProperties: map[string]interface{}{
							"deployment": map[string]interface{}{
								"strategy": "blue-green",
								"region":   "eastus",
							},
						},
					},
				},
			},
		},
		{
			"combined-extensions",
			&ProjectConfig{
				Name: "test-combined-extensions",
				Services: map[string]*ServiceConfig{
					"api": {
						Language:     ServiceLanguageJavaScript,
						Host:         ContainerAppTarget,
						RelativePath: "./src/api",
						AdditionalProperties: map[string]interface{}{
							"serviceExtension": "api-specific",
							"customConfig": map[string]interface{}{
								"setting1": "value1",
							},
						},
					},
				},
				AdditionalProperties: map[string]interface{}{
					"projectExtension": "global-setting",
					"sharedConfig": map[string]interface{}{
						"environment": "production",
						"version":     "1.0.0",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()

			// First save: write the constructed project to YAML
			firstSaveFile := filepath.Join(tempDir, "azure-first.yaml")
			err := Save(context.Background(), tt.project, firstSaveFile)
			require.NoError(t, err)

			// Load the project back (this initializes all internal fields properly)
			loadedProject, err := Load(context.Background(), firstSaveFile)
			require.NoError(t, err)

			// Second save: save the loaded project to verify round-trip
			secondSaveFile := filepath.Join(tempDir, "azure-second.yaml")
			err = Save(context.Background(), loadedProject, secondSaveFile)
			require.NoError(t, err)

			// Load the second save and compare with first loaded project
			reloadedProject, err := Load(context.Background(), secondSaveFile)
			require.NoError(t, err)

			// Verify round-trip preservation with deep equality
			assert.Equal(t, loadedProject, reloadedProject)

			// Snapshot the marshalled output to verify structure
			savedContents, err := os.ReadFile(firstSaveFile)
			require.NoError(t, err)
			snapshot.SnapshotT(t, string(savedContents))
		})
	}
}

// ExtensionConfig represents a type-safe configuration structure that an extension might define
type ExtensionConfig struct {
	Timeout  int                    `yaml:"timeout"`
	Retries  int                    `yaml:"retries"`
	Database DatabaseConfig         `yaml:"database"`
	Features map[string]interface{} `yaml:"features,omitempty"`
}

type DatabaseConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

func TestAdditionalPropertiesExtraction(t *testing.T) {
	// Create a project with AdditionalProperties that includes extension configuration
	project := &ProjectConfig{
		Name: "test-extension-extraction",
		Services: map[string]*ServiceConfig{
			"api": {
				Language:     ServiceLanguageJavaScript,
				Host:         ContainerAppTarget,
				RelativePath: "./src/api",
				AdditionalProperties: map[string]interface{}{
					"customServiceField": "service-extension",
					"monitoring": map[string]interface{}{
						"enabled": true,
						"level":   "verbose",
					},
				},
			},
		},
		AdditionalProperties: map[string]interface{}{
			"customProjectField": "project-extension",
			"extensionConfig": map[string]interface{}{
				"timeout": 300,
				"retries": 3,
				"database": map[string]interface{}{
					"host": "localhost",
					"port": 5432,
				},
				"features": map[string]interface{}{
					"caching":    true,
					"monitoring": false,
				},
			},
			"otherExtension": map[string]interface{}{
				"setting1": "value1",
				"setting2": 42,
			},
		},
	}

	t.Run("ExtractProjectLevelConfig", func(t *testing.T) {
		// Create a config from the AdditionalProperties map
		cfg := config.NewConfig(project.AdditionalProperties)

		// Extract the extensionConfig section using GetSection
		var extensionConfig ExtensionConfig
		found, err := cfg.GetSection("extensionConfig", &extensionConfig)
		require.NoError(t, err)
		require.True(t, found, "extensionConfig section should be found")

		// Verify the type-safe configuration was extracted correctly
		assert.Equal(t, 300, extensionConfig.Timeout)
		assert.Equal(t, 3, extensionConfig.Retries)
		assert.Equal(t, "localhost", extensionConfig.Database.Host)
		assert.Equal(t, 5432, extensionConfig.Database.Port)
		assert.Equal(t, true, extensionConfig.Features["caching"])
		assert.Equal(t, false, extensionConfig.Features["monitoring"])
	})

	t.Run("ExtractServiceLevelConfig", func(t *testing.T) {
		apiService := project.Services["api"]
		require.NotNil(t, apiService)

		// Create a config from the service AdditionalProperties
		serviceCfg := config.NewConfig(apiService.AdditionalProperties)

		// Define a type-safe structure for monitoring config
		type MonitoringConfig struct {
			Enabled bool   `yaml:"enabled"`
			Level   string `yaml:"level"`
		}

		// Extract monitoring configuration using GetSection
		var monitoringConfig MonitoringConfig
		found, err := serviceCfg.GetSection("monitoring", &monitoringConfig)
		require.NoError(t, err)
		require.True(t, found, "monitoring section should be found")

		// Verify the type-safe configuration
		assert.True(t, monitoringConfig.Enabled)
		assert.Equal(t, "verbose", monitoringConfig.Level)
	})

	t.Run("RoundTripWithExtractedConfig", func(t *testing.T) {
		tempDir := t.TempDir()

		// Save the original project
		originalFile := filepath.Join(tempDir, "original.yaml")
		err := Save(context.Background(), project, originalFile)
		require.NoError(t, err)

		// Load it back
		loadedProject, err := Load(context.Background(), originalFile)
		require.NoError(t, err)

		// Extract the extension config using the config system
		cfg := config.NewConfig(loadedProject.AdditionalProperties)
		var extensionConfig ExtensionConfig
		found, err := cfg.GetSection("extensionConfig", &extensionConfig)
		require.NoError(t, err)
		require.True(t, found, "extensionConfig section should be found")

		// Modify the configuration
		extensionConfig.Timeout = 600
		extensionConfig.Database.Host = "production-db"

		// Create a new config from the modified struct and extract as raw map
		modifiedCfg := config.NewConfig(map[string]interface{}{
			"extensionConfig": extensionConfig,
		})
		modifiedRaw := modifiedCfg.Raw()

		loadedProject.AdditionalProperties["extensionConfig"] = modifiedRaw["extensionConfig"]

		// Save the modified project
		modifiedFile := filepath.Join(tempDir, "modified.yaml")
		err = Save(context.Background(), loadedProject, modifiedFile)
		require.NoError(t, err)

		// Load and verify the changes were preserved
		finalProject, err := Load(context.Background(), modifiedFile)
		require.NoError(t, err)

		// Extract the final config using the config system
		finalCfg := config.NewConfig(finalProject.AdditionalProperties)
		var finalExtensionConfig ExtensionConfig
		found, err = finalCfg.GetSection("extensionConfig", &finalExtensionConfig)
		require.NoError(t, err)
		require.True(t, found, "extensionConfig section should be found")

		// Verify the modifications were preserved
		assert.Equal(t, 600, finalExtensionConfig.Timeout)
		assert.Equal(t, "production-db", finalExtensionConfig.Database.Host)
		assert.Equal(t, 3, finalExtensionConfig.Retries) // Unchanged
	})
}
