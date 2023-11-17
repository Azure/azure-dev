// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
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
		convert.RefOf("rg-test"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("deployedApiSvc"),
				Name:     convert.RefOf("deployedApiSvc"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus2"),
			},
		})
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	depOpService := mockazcli.NewDeploymentOperationsServiceFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, azCli, depOpService)
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
		convert.RefOf("rg-test"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("app-api-abc123"),
				Name:     &resourceName,
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus2"),
				Tags: map[string]*string{
					azure.TagKeyAzdServiceName: convert.RefOf("api"),
				},
			},
		},
	)
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	depOpService := mockazcli.NewDeploymentOperationsServiceFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, azCli, depOpService)
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
				ID:       convert.RefOf("deployedApiSvc"),
				Name:     convert.RefOf("deployedApiSvc"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus2"),
			},
			{
				ID:       convert.RefOf("webResource"),
				Name:     convert.RefOf("webResource"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus2"),
				Tags: map[string]*string{
					azure.TagKeyAzdServiceName: convert.RefOf("web"),
				},
			},
		})
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	depOpService := mockazcli.NewDeploymentOperationsServiceFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, azCli, depOpService)

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
				ID:       convert.RefOf("deployedApiSvc"),
				Name:     convert.RefOf("deployedApiSvc"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus2"),
			},
			{
				ID:       convert.RefOf("webResource"),
				Name:     convert.RefOf("webResource"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus2"),
				Tags: map[string]*string{
					azure.TagKeyAzdServiceName: convert.RefOf("web"),
				},
			},
		})
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)
	depOpService := mockazcli.NewDeploymentOperationsServiceFromMockContext(mockContext)

	env := environment.NewWithValues("envA", map[string]string{
		environment.ResourceGroupEnvVarName:  expectedResourceGroupName,
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, azCli, depOpService)
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
