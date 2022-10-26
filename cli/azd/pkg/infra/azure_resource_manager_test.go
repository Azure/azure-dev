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
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

var mockSubDeploymentOperations string = `
{
	"nextLink":"",
	"value": [
		{
			"id": "resource-group-id",
			"operationId": "foo",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Resources/resourceGroups",
					"id":"resource-group-id",
					"resourceName": "resource-group-name"
				}
			}
		},
		{
			"id": "deployment-id",
			"operationId": "foo",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Resources/deployments",
					"id":"group-deployment-id",
					"resourceName": "group-deployment-id"
				}
			}
		}
	]
}
`

var mockGroupDeploymentOperations string = `
{
	"nextLink":"",
	"value": [
		{
			"id": "website-resource-id",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Web/sites",
					"id":"website-resource-id",
					"resourceName": "website-resource-name"
				}
			}
		},
		{
			"id": "storage-resource-id",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Storage/storageAccounts",
					"id":"storage-resource-id",
					"resourceName": "storage-resource-name"
				}
			}
		}
	]
}
`

var mockNestedGroupDeploymentOperations string = `
{
	"nextLink":"",
	"value": [
		{
			"id": "website-resource-id",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Web/sites",
					"id":"website-resource-id",
					"resourceName": "website-resource-name"
				}
			}
		},
		{
			"id": "storage-resource-id",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Storage/storageAccounts",
					"id":"storage-resource-id",
					"resourceName": "storage-resource-name"
				}
			}
		},
		{
			"id": "nested-deployment-id",
			"properties": {
				"provisioningOperation":"Create",
				"targetResource": {
					"resourceType": "Microsoft.Resources/deployments",
					"id":"nested-group-deployment-id",
					"resourceName": "nested-group-deployment-name"
				}
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

func TestGetDeploymentResourceOperationsSuccess(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	mockContext := mocks.NewMockContext(context.Background())
	scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/resourcegroups/resource-group-name/deployments/group-deployment-id/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		subCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockGroupDeploymentOperations))),
			Request: &http.Request{
				Method: http.MethodGet,
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
			},
		}, nil
	})

	arm := NewAzureResourceManager(*mockContext.Context)
	operations, err := arm.GetDeploymentResourceOperations(*mockContext.Context, scope)
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
	scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

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
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockSubDeploymentOperations))),
			Request: &http.Request{
				Method: http.MethodGet,
			},
		}, nil
	})

	arm := NewAzureResourceManager(*mockContext.Context)
	operations, err := arm.GetDeploymentResourceOperations(*mockContext.Context, scope)

	require.Nil(t, operations)
	require.NotNil(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "getting subscription deployment"))
	require.Equal(t, 1, subCalls)
	require.Equal(t, 0, groupCalls)
}

func TestGetDeploymentResourceOperationsNoResourceGroup(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	mockContext := mocks.NewMockContext(context.Background())
	scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

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
			},
		}, nil
	})

	arm := NewAzureResourceManager(*mockContext.Context)
	operations, err := arm.GetDeploymentResourceOperations(*mockContext.Context, scope)

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
	scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

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
			Body:       io.NopCloser(bytes.NewBuffer([]byte(mockNestedGroupDeploymentOperations))),
			Request: &http.Request{
				Method: http.MethodGet,
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
			},
		}, nil
	})

	arm := NewAzureResourceManager(*mockContext.Context)
	operations, err := arm.GetDeploymentResourceOperations(*mockContext.Context, scope)

	require.NotNil(t, operations)
	require.Nil(t, err)
	require.Len(t, operations, 6)
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
				ID:       convert.RefOf(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", SUBSCRIPTION_ID, name)),
				Type:     convert.RefOf("Microsoft.Resources/resourceGroups"),
				Name:     convert.RefOf(name),
				Location: convert.RefOf("eastus2"),
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

			env := environment.EphemeralWithValues("test-env", map[string]string{
				"AZURE_SUBSCRIPTION_ID": SUBSCRIPTION_ID,
			})

			arm := NewAzureResourceManager(*mockContext.Context)
			rgName, err := arm.FindResourceGroupForEnvironment(*mockContext.Context, env)

			if tt.expectedErrorText == "" {
				require.NoError(t, err)
				require.Equal(t, tt.expectedName, rgName)
			} else {
				require.ErrorContains(t, err, tt.expectedErrorText)
			}
		})
	}
}
