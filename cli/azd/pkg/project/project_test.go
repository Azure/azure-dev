// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
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

	env := environment.EphemeralWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectMangaer := NewProjectManager(nil)
	projectConfig, err := projectMangaer.Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, azCli)
	targetResource, err := resourceManager.GetTargetResource(*mockContext.Context, projectConfig.Services["api"])
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
					defaultServiceTag: convert.RefOf("api"),
				},
			},
		},
	)
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	env := environment.EphemeralWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectMangaer := NewProjectManager(nil)
	projectConfig, err := projectMangaer.Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, azCli)
	targetResource, err := resourceManager.GetTargetResource(*mockContext.Context, projectConfig.Services["api"])
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
					defaultServiceTag: convert.RefOf("web"),
				},
			},
		})
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	env := environment.EphemeralWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectMangaer := NewProjectManager(nil)
	projectConfig, err := projectMangaer.Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, azCli)

	for _, svc := range projectConfig.Services {
		targetResource, err := resourceManager.GetTargetResource(*mockContext.Context, svc)
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
					defaultServiceTag: convert.RefOf("web"),
				},
			},
		})
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	env := environment.EphemeralWithValues("envA", map[string]string{
		environment.ResourceGroupEnvVarName:  expectedResourceGroupName,
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectMangaer := NewProjectManager(nil)
	projectConfig, err := projectMangaer.Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	resourceManager := NewResourceManager(env, azCli)
	targetResource, err := resourceManager.GetTargetResource(*mockContext.Context, projectConfig.Services["api"])
	require.NoError(t, err)
	require.NotNil(t, targetResource)

	for _, svc := range projectConfig.Services {
		targetResource, err := resourceManager.GetTargetResource(*mockContext.Context, svc)
		require.NoError(t, err)
		require.NotNil(t, targetResource)
		require.Equal(t, expectedResourceGroupName, targetResource.ResourceGroupName())
	}
}
