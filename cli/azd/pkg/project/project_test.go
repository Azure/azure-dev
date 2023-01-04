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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
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

	e := environment.EphemeralWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectConfig, err := ParseProjectConfig(testProj)
	require.NoError(t, err)

	project, err := projectConfig.GetProject(*mockContext.Context, e, mockContext.Console, azCli, mockContext.CommandRunner)
	require.NoError(t, err)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.TargetResource.ResourceName() == "deployedApiSvc" },
		"api service does not have expected resource name",
	)
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

	e := environment.EphemeralWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectConfig, err := ParseProjectConfig(testProj)
	require.NoError(t, err)

	project, err := projectConfig.GetProject(*mockContext.Context, e, mockContext.Console, azCli, mockContext.CommandRunner)
	require.NoError(t, err)

	// Deployment resource name comes from the found tag on the graph query request
	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.TargetResource.ResourceName() == resourceName },
		"api service does not have expected resource name",
	)
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

	e := environment.EphemeralWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectConfig, err := ParseProjectConfig(testProj)
	require.NoError(t, err)

	project, err := projectConfig.GetProject(*mockContext.Context, e, mockContext.Console, azCli, mockContext.CommandRunner)
	require.NoError(t, err)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.TargetResource.ResourceGroupName() == resourceGroupName },
		"api service does not have expected resource group name",
	)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.TargetResource.ResourceGroupName() == resourceGroupName },
		"web service does not have expected resource group name",
	)
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

	e := environment.EphemeralWithValues("envA", map[string]string{
		environment.ResourceGroupEnvVarName:  expectedResourceGroupName,
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := ParseProjectConfig(testProj)
	require.NoError(t, err)

	project, err := projectConfig.GetProject(*mockContext.Context, e, mockContext.Console, azCli, mockContext.CommandRunner)
	require.NoError(t, err)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.TargetResource.ResourceGroupName() == expectedResourceGroupName },
		"api service does not have expected resource group name",
	)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.TargetResource.ResourceGroupName() == expectedResourceGroupName },
		"web service does not have expected resource group name",
	)
}

func assertHasService(t *testing.T, ss []*Service, match func(*Service) bool, msgAndArgs ...interface{}) {
	i := slices.IndexFunc(ss, match)
	assert.GreaterOrEqual(t, i, 0, msgAndArgs)
}
