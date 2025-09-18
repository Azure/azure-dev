// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/stretchr/testify/require"
)

//nolint:lll
var mockSubDeploymentOperations string = `
{
	"nextLink":"",
	"value": [
		{
			"id": "resource-group-id",
			"operationId": "foo1",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Resources/resourceGroups",
					"id":"/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name",
					"resourceName": "resource-group-name"
				},
				"timestamp":"9999-10-31T14:00:00Z"
			}
		},
		{
			"id": "deployment-id",
			"operationId": "foo2",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Resources/deployments",
					"id":"/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/Microsoft.Resources/deployments/group-deployment-name",
					"resourceName": "group-deployment-name"
				},
				"timestamp":"9999-10-31T14:00:00Z"
			}
		},
		{
			"id": "resource-id",
			"operationId": "foo3",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.KeyVault/vaults",
					"id":"/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/Microsoft.KeyVault/vaults/keyvault-resource-name",
					"resourceName": "keyvault-resource-name"
				},
				"timestamp":"9999-10-31T14:00:00Z"
			}
		}
	]
}
`

//nolint:lll
var mockGroupDeploymentOperations string = `
{
	"nextLink":"",
	"value": [
		{
			"id": "website-resource-id",
			"operationId": "foo3",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Web/sites",
					"id":"/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/Microsoft.Web/sites/website-resource-name",
					"resourceName": "website-resource-name"
				},
				"timestamp":"9999-10-31T14:00:00Z"
			}
		},
		{
			"id": "storage-resource-id",
			"operationId": "foo4",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Storage/storageAccounts",
					"id":"/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/Microsoft.Storage/storageAccounts/storage-resource-name",
					"resourceName": "storage-resource-name"
				},
				"timestamp":"9999-10-31T14:00:00Z"
			}
		}
	]
}
`

//nolint:lll
var mockNestedGroupDeploymentOperations string = `
{
	"nextLink":"",
	"value": [
		{
			"id": "website-resource-id",
			"operationId": "foo5",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Web/sites",
					"id":"/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/Microsoft.Web/sites/website-resource-name",
					"resourceName": "website-resource-name"
				},
				"timestamp":"9999-10-31T14:00:00Z"
			}
		},
		{
			"id": "storage-resource-id",
			"operationId": "foo6",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Storage/storageAccounts",
					"id":"/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/Microsoft.Storage/storageAccounts/storage-resource-name",
					"resourceName": "storage-resource-name"
				},
				"timestamp":"9999-10-31T14:00:00Z"
			}
		},
		{
			"id": "nested-deployment-id",
			"operationId": "foo7",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Resources/deployments",
					"id":"/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/Microsoft.Resources/deployments/nested-group-deployment-name",
					"resourceName": "nested-group-deployment-name"
				},
				"timestamp":"9999-10-31T14:00:00Z"
			}
		}
	]
}
`

var mockSubDeploymentOperationsEmpty string = `
{
	"nextLink":"",
	"value": []
}
`
var qStart = time.Now()

func TestGetDeploymentResourceOperationsSuccess(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	mockContext := mocks.NewMockContext(context.Background())
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)
	scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
	deployment := NewSubscriptionDeployment(
		scope,
		"DEPLOYMENT_NAME",
	)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/resourcegroups/resource-group-name/deployments/group-deployment-name/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		subCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockGroupDeploymentOperations))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		groupCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockSubDeploymentOperations))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})

	arm := NewAzureResourceManager(resourceService, deploymentService)
	operations, err := arm.GetDeploymentResourceOperations(*mockContext.Context, deployment, &qStart)
	require.NotNil(t, operations)
	require.Nil(t, err)

	require.Len(t, operations, 4)
	require.Equal(t, 1, subCalls)
	require.Equal(t, 1, groupCalls)
}

func TestGetDeploymentResourceOperationsFail(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	mockContext := mocks.NewMockContext(context.Background())
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)
	scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
	deployment := NewSubscriptionDeployment(
		scope,
		"DEPLOYMENT_NAME",
	)

	/*NOTE: Mocking first response as an `StatusForbidden` error which is not retried by the sdk client.
	  Adding an extra mock to test that it is not called*/
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		subCalls++
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL: &url.URL{
					Scheme: "https",
					Host:   "management.azure.com",
					Path:   "/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME",
				},
			},
		}, nil
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/resourcegroups/resource-group-name/deployments/group-deployment-name/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		groupCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockSubDeploymentOperations))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})

	arm := NewAzureResourceManager(resourceService, deploymentService)
	operations, err := arm.GetDeploymentResourceOperations(*mockContext.Context, deployment, &qStart)

	require.Nil(t, operations)
	require.NotNil(t, err)
	require.ErrorContains(t, err, "failed getting list of deployment operations from subscription")
	require.Equal(t, 1, subCalls)
	require.Equal(t, 0, groupCalls)
}

func TestGetDeploymentResourceOperationsNoResourceGroup(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	mockContext := mocks.NewMockContext(context.Background())
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)
	scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
	deployment := NewSubscriptionDeployment(
		scope,
		"DEPLOYMENT_NAME",
	)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		subCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockSubDeploymentOperationsEmpty))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/resourcegroups/resource-group-name/deployments/group-deployment-id/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		groupCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockSubDeploymentOperationsEmpty))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})

	arm := NewAzureResourceManager(resourceService, deploymentService)
	operations, err := arm.GetDeploymentResourceOperations(*mockContext.Context, deployment, &qStart)

	require.NotNil(t, operations)
	require.Nil(t, err)
	require.Len(t, operations, 0)
	require.Equal(t, 1, subCalls)
	require.Equal(t, 0, groupCalls)
}

func TestGetDeploymentResourceOperationsWithNestedDeployments(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	mockContext := mocks.NewMockContext(context.Background())
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)
	scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
	deployment := NewSubscriptionDeployment(
		scope,
		"DEPLOYMENT_NAME",
	)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		subCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockSubDeploymentOperations))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/resourcegroups/resource-group-name/deployments/group-deployment-name/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		groupCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockNestedGroupDeploymentOperations))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/resourcegroups/resource-group-name"+
				"/deployments/nested-group-deployment-name/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		groupCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockGroupDeploymentOperations))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})

	arm := NewAzureResourceManager(resourceService, deploymentService)
	operations, err := arm.GetDeploymentResourceOperations(*mockContext.Context, deployment, &qStart)

	require.NotNil(t, operations)
	require.Nil(t, err)
	require.Len(t, operations, 4)
	require.Equal(t, 1, subCalls)
	require.Equal(t, 2, groupCalls)
}

func TestFindResourceGroupForEnvironment(t *testing.T) {
	t.Parallel()

	const SUBSCRIPTION_ID = "273f1e6b-6c19-4c9e-8b67-5fbe78b14063"

	// builds a 200 OK response with a list of resource groups in the body.
	responseForGroups := func(groupNames []string) *http.Response {
		res := &armresources.ResourceGroupListResult{
			Value: make([]*armresources.ResourceGroup, 0),
		}

		for _, name := range groupNames {
			res.Value = append(res.Value, &armresources.ResourceGroup{
				ID:       to.Ptr(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", SUBSCRIPTION_ID, name)),
				Type:     to.Ptr("Microsoft.Resources/resourceGroups"),
				Name:     to.Ptr(name),
				Location: to.Ptr("eastus2"),
			})
		}

		body, err := json.Marshal(res)
		if err != nil {
			panic(err)
		}

		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader(body)),
		}
	}

	tests := []struct {
		name              string
		rgsFromFilter     []string
		rgsFromNoFilter   []string
		expectedName      string
		expectedErrorText string
	}{
		{"oneTaggedResourceGroup", []string{"custom-rg-name"}, nil, "custom-rg-name", ""},
		{"noTagButPrefix", nil, []string{"rg-test-env"}, "rg-test-env", ""},
		{"noTagButSuffix", nil, []string{"test-env-rg"}, "test-env-rg", ""},
		{
			"twoTaggedResourceGroups",
			[]string{"custom-rg-name", "custom-rg-name-2"},
			nil,
			"",
			"more than one possible resource group was found",
		},
		{"noResourceGroups", nil, nil, "", "0 resource groups with prefix or suffix with value"},
		{
			"noTagMultipleMatches",
			nil,
			[]string{"test-env-rg", "rg-test-env"},
			"",
			"more than one possible resource group was found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			resourceService := azapi.NewResourceService(
				mockContext.SubscriptionCredentialProvider,
				mockContext.ArmClientOptions,
			)
			deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)

			mockContext.HttpClient.When(func(request *http.Request) bool {
				return request.Method == "GET" && strings.Contains(request.URL.Path, "/resourcegroups") &&
					request.URL.Query().Has("$filter")
			}).RespondFn(func(request *http.Request) (*http.Response, error) {
				require.Contains(t, request.URL.Query().Get("$filter"), "tagName eq 'azd-env-name'")
				require.Contains(t, request.URL.Query().Get("$filter"), "tagValue eq 'test-env'")

				return responseForGroups(tt.rgsFromFilter), nil
			})
			mockContext.HttpClient.When(func(request *http.Request) bool {
				return request.Method == "GET" && strings.Contains(request.URL.Path, "/resourcegroups") &&
					!request.URL.Query().Has("$filter")
			}).RespondFn(func(request *http.Request) (*http.Response, error) {
				return responseForGroups(tt.rgsFromNoFilter), nil
			})

			env := environment.NewWithValues("test-env", map[string]string{
				"AZURE_SUBSCRIPTION_ID": SUBSCRIPTION_ID,
			})

			arm := NewAzureResourceManager(resourceService, deploymentService)
			rgName, err := arm.FindResourceGroupForEnvironment(
				*mockContext.Context, env.GetSubscriptionId(), env.Name())

			if tt.expectedErrorText == "" {
				require.NoError(t, err)
				require.Equal(t, tt.expectedName, rgName)
			} else {
				require.ErrorContains(t, err, tt.expectedErrorText)
			}
		})
	}
}
