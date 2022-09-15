// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
    host: containerapp
`
	mockContext := mocks.NewMockContext(context.Background())

	e := environment.EphemeralWithValues("envA", nil)
	projectConfig, err := ParseProjectConfig(testProj, e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(mockContext.Context, e)
	assert.Nil(t, err)

	azCli := azcli.GetAzCli(*mockContext.Context)

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

	sha := sha256.Sum256([]byte("test-proj-template"))
	hash := hex.EncodeToString(sha[:])
	require.Contains(t, azCli.UserAgent(), "azdtempl/"+hash)
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

	e := environment.EphemeralWithValues("envA", nil)
	projectConfig, err := ParseProjectConfig(testProj, e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(mockContext.Context, e)
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
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.AddAzResourceListMock(&rg,
		[]azcli.AzCliResource{
			{
				Id:       "random",
				Name:     resourceName,
				Type:     string(infra.AzureResourceTypeWebSite),
				Location: "westus2",
			}})

	e := environment.EphemeralWithValues("envA", nil)
	projectConfig, err := ParseProjectConfig(testProj, e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(mockContext.Context, e)
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

	e := environment.EphemeralWithValues("envA", nil)
	projectConfig, err := ParseProjectConfig(testProj, e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(mockContext.Context, e)
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

	expectedResourceGroupName := "custom-name-from-env-rg"

	e := environment.EphemeralWithValues("envA", map[string]string{
		"AZURE_RESOURCE_GROUP": expectedResourceGroupName,
	})

	projectConfig, err := ParseProjectConfig(testProj, e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(mockContext.Context, e)
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
