// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/httpUtil"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

var gblCmdOptions = &commands.GlobalCommandOptions{
	EnableDebugLogging: false,
	EnableTelemetry:    true,
}

var azCli = azcli.NewAzCli(azcli.NewAzCliArgs{
	EnableDebug:     false,
	EnableTelemetry: true,
	RunWithResultFn: func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
		if helpers.CallStackContains("GetAccessToken") {
			now := time.Now().UTC().Format(time.RFC3339)
			requestJson := fmt.Sprintf(`{"AccessToken": "abc123", "ExpiresOn": "%s"}`, now)
			return executil.NewRunResult(0, requestJson, ""), nil
		}

		return executil.NewRunResult(0, "", ""), nil
	},
})

var mockHttpClient = &helpers.MockHttpUtil{
	SendRequestFn: func(req *httpUtil.HttpRequestMessage) (*httpUtil.HttpResponseMessage, error) {
		if req.Method == http.MethodPost && strings.Contains(req.Url, "providers/Microsoft.ResourceGraph/resources") {
			jsonResponse := `{"data": [], "total_records": 0}`

			response := &httpUtil.HttpResponseMessage{
				Status: 200,
				Body:   []byte(jsonResponse),
			}

			return response, nil
		}

		return nil, fmt.Errorf("Mock not registered for request")
	},
}

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
	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("envA")
	projectConfig, err := ParseProjectConfig(testProj, &e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(ctx, &e)
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

	require.Contains(t, azCli.UserAgent(), "azdtempl/test-proj-template")
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

	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("envA")
	projectConfig, err := ParseProjectConfig(testProj, &e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(ctx, &e)
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
	graphQueryResult := &azcli.AzCliGraphQuery{
		Count:        1,
		TotalRecords: 1,
		Data: []azcli.AzCliResource{
			{
				Id:       "random",
				Name:     "app-api-abc123",
				Type:     string(infra.AzureResourceTypeWebSite),
				Location: "westus2",
			},
		},
	}

	var mockHttpClient = &helpers.MockHttpUtil{
		SendRequestFn: func(req *httpUtil.HttpRequestMessage) (*httpUtil.HttpResponseMessage, error) {
			if req.Method == http.MethodPost && strings.Contains(req.Url, "providers/Microsoft.ResourceGraph/resources") {
				var jsonResponse string
				bytes, err := json.Marshal(graphQueryResult)
				if err == nil {
					jsonResponse = string(bytes)
				}

				response := &httpUtil.HttpResponseMessage{
					Status: 200,
					Body:   []byte(jsonResponse),
				}

				return response, nil
			}

			return nil, fmt.Errorf("Mock not registered for request")
		},
	}

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

	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("envA")
	projectConfig, err := ParseProjectConfig(testProj, &e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(ctx, &e)
	assert.Nil(t, err)

	// Deployment resource name comes from the found tag on the graph query request
	assertHasService(t,
		project.Services,
		func(s *Service) bool { return s.Scope.ResourceName() == graphQueryResult.Data[0].Name },
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

	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("envA")
	projectConfig, err := ParseProjectConfig(testProj, &e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(ctx, &e)
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

	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)

	expectedResourceGroupName := "custom-name-from-env-rg"
	values := map[string]string{"AZURE_RESOURCE_GROUP": expectedResourceGroupName}
	e := environment.Environment{Values: values}

	e.SetEnvName("envA")
	projectConfig, err := ParseProjectConfig(testProj, &e)
	assert.Nil(t, err)

	project, err := projectConfig.GetProject(ctx, &e)
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
