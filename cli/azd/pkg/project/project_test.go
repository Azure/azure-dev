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
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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
	deploymentService := mockazcli.NewStandardDeploymentsFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, deploymentService, resourceService)
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
	deploymentService := mockazcli.NewStandardDeploymentsFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, deploymentService, resourceService)
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
	deploymentService := mockazcli.NewStandardDeploymentsFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, deploymentService, resourceService)

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
	deploymentService := mockazcli.NewStandardDeploymentsFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.ResourceGroupEnvVarName:  expectedResourceGroupName,
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, deploymentService, resourceService)
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

		hooksPath := filepath.Join(infraPath, "azd.hooks.yaml")
		hooksContent := []byte(`
pre-build:
  shell: sh
  run: ./pre-build.sh
post-build:
  shell: pwsh
  run: ./post-build.ps1
`)

		err = os.WriteFile(hooksPath, hooksContent, osutil.PermissionDirectory)
		require.NoError(t, err)

		expectedHooks := HooksConfig{
			"pre-build": {{
				Name:            "",
				Shell:           ext.ShellTypeBash,
				Run:             "./pre-build.sh",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			}},
			"post-build": {{
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

	t.Run("ErrorDoubleDefintionHooks", func(t *testing.T) {
		prj := &ProjectConfig{
			Name:     "minimal",
			Services: map[string]*ServiceConfig{},
			Hooks: HooksConfig{
				"prebuild": {{
					Run: "./pre-build.sh",
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

		hooksPath := filepath.Join(infraPath, "azd.hooks.yaml")
		hooksContent := []byte(`
pre-build:
  shell: sh
  run: ./pre-build.sh
post-build:
  shell: pwsh
  run: ./post-build.ps1
`)

		err = os.WriteFile(hooksPath, hooksContent, osutil.PermissionDirectory)
		require.NoError(t, err)

		project, err := Load(context.Background(), azureYamlPath)
		require.Error(t, err)
		var expectedProject *ProjectConfig
		require.Equal(t, expectedProject, project)
	})

	t.Run("ServiceInfraHooks", func(t *testing.T) {
		tempDir := t.TempDir()

		prj := &ProjectConfig{
			Name: "minimal",
			Services: map[string]*ServiceConfig{
				"api": {
					Name:         "api",
					Host:         AppServiceTarget,
					RelativePath: filepath.Join(tempDir, "api"),
				},
			},
		}
		contents, err := yaml.Marshal(prj)
		require.NoError(t, err)

		azureYamlPath := filepath.Join(tempDir, "azure.yaml")
		err = os.WriteFile(azureYamlPath, contents, osutil.PermissionDirectory)
		require.NoError(t, err)

		servicePath := filepath.Join(tempDir, "api")
		err = os.Mkdir(servicePath, osutil.PermissionDirectory)
		require.NoError(t, err)

		hooksPath := filepath.Join(servicePath, "azd.hooks.yaml")
		hooksContent := []byte(`
pre-build:
  shell: sh
  run: ./pre-build.sh
post-build:
  shell: pwsh
  run: ./post-build.ps1
`)

		err = os.WriteFile(hooksPath, hooksContent, osutil.PermissionDirectory)
		require.NoError(t, err)

		expectedHooks := HooksConfig{
			"pre-build": {{
				Name:            "",
				Shell:           ext.ShellTypeBash,
				Run:             "./pre-build.sh",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			}},
			"post-build": {{
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
		require.Equal(t, expectedHooks, project.Services["api"].Hooks)
	})

	t.Run("ErrorDoubleDefintionServiceHooks", func(t *testing.T) {
		tempDir := t.TempDir()
		prj := &ProjectConfig{
			Name: "minimal",
			Services: map[string]*ServiceConfig{
				"api": {
					Name: "api",
					Host: AppServiceTarget,
					Hooks: HooksConfig{
						"prebuild": {{
							Run: "./pre-build.sh",
						}},
					},
					RelativePath: filepath.Join(tempDir, "api"),
				},
			},
		}
		contents, err := yaml.Marshal(prj)
		require.NoError(t, err)

		azureYamlPath := filepath.Join(tempDir, "azure.yaml")
		err = os.WriteFile(azureYamlPath, contents, osutil.PermissionDirectory)
		require.NoError(t, err)

		servicePath := filepath.Join(tempDir, "api")
		err = os.Mkdir(servicePath, osutil.PermissionDirectory)
		require.NoError(t, err)

		hooksPath := filepath.Join(servicePath, "azd.hooks.yaml")
		hooksContent := []byte(`
pre-build:
  shell: sh
  run: ./pre-build.sh
post-build:
  shell: pwsh
  run: ./post-build.ps1
`)

		err = os.WriteFile(hooksPath, hooksContent, osutil.PermissionDirectory)
		require.NoError(t, err)

		project, err := Load(context.Background(), azureYamlPath)
		require.Error(t, err)
		var expectedProject *ProjectConfig
		require.Equal(t, expectedProject, project)
	})
}
