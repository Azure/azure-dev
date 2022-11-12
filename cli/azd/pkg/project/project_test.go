// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"golang.org/x/exp/slices"
)

// If resource name is not specified, it should default to <environment name><service friendly name>
func TestResourceNameDefaultValues(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: appservice
  api:
    project: src/api
    language: js
    host: appservice
  worker:
    project: src/worker
    language: js
    host: containerapp
`
	mockContext := mocks.NewMockContext(context.Background())
	azCli := newAzCliFromMockContext(mockContext)

	e := environment.EphemeralWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectConfig, err := ParseProjectConfig(testProj, e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(mockContext.Context, e, azCli)
	assert.Nil(t, err)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceName() == "envAapi" },
		"api service does not have expected resource name",
	)
	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceName() == "envAweb" },
		"web service does not have expected resource name",
	)
	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceName() == "envAworker" },
		"worker service does not have expected resource name",
	)
}

// Specifying resource name in the project file should override the default
func TestResourceNameOverrideFromProjectFile(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
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
	azCli := newAzCliFromMockContext(mockContext)

	e := environment.EphemeralWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectConfig, err := ParseProjectConfig(testProj, e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(mockContext.Context, e, azCli)
	assert.Nil(t, err)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceName() == "deployedApiSvc" },
		"api service does not have expected resource name",
	)

	// deploymentBaseName is not specified, the default name should be used
	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceName() == "envAweb" },
		"web service does not have expected resource name",
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
	rg := "rg-test"
	resourceName := "app-api-abc123"
	resourceId := "random"
	resourceType := string(infra.AzureResourceTypeWebSite)
	resourceLocation := "westus2"
	mockContext := mocks.NewMockContext(context.Background())
	azCli := newAzCliFromMockContext(mockContext)

	mockContext.HttpClient.AddAzResourceListMock(&rg,
		armresources.ResourceListResult{
			Value: []*armresources.GenericResourceExpanded{
				{
					ID:       &resourceId,
					Name:     &resourceName,
					Type:     &resourceType,
					Location: &resourceLocation,
				},
			},
		})

	e := environment.EphemeralWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectConfig, err := ParseProjectConfig(testProj, e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(mockContext.Context, e, azCli)
	assert.Nil(t, err)

	// Deployment resource name comes from the found tag on the graph query request
	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceName() == resourceName },
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
	azCli := newAzCliFromMockContext(mockContext)

	e := environment.EphemeralWithValues("envA", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})
	projectConfig, err := ParseProjectConfig(testProj, e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(mockContext.Context, e, azCli)
	assert.Nil(t, err)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceGroupName() == "rg-custom-group" },
		"api service does not have expected resource group name",
	)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceGroupName() == "rg-custom-group" },
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
	azCli := newAzCliFromMockContext(mockContext)

	expectedResourceGroupName := "custom-name-from-env-rg"

	e := environment.EphemeralWithValues("envA", map[string]string{
		environment.ResourceGroupEnvVarName:  expectedResourceGroupName,
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	projectConfig, err := ParseProjectConfig(testProj, e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(mockContext.Context, e, azCli)
	assert.Nil(t, err)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceGroupName() == expectedResourceGroupName },
		"api service does not have expected resource group name",
	)

	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceGroupName() == expectedResourceGroupName },
		"web service does not have expected resource group name",
	)
}

func assertHasService(t *testing.T, ss []*Service, match func(*Service) bool, msgAndArgs ...interface{}) {
	i := slices.IndexFunc(ss, match)
	assert.GreaterOrEqual(t, i, 0, msgAndArgs)
}
