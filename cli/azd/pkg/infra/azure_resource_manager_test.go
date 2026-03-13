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
	"regexp"
	"strings"
	"sync"
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

func createDeploymentOperationResponse(t *testing.T, operations ...*armresources.DeploymentOperation) string {
	t.Helper()

	response := armresources.DeploymentOperationsListResult{
		Value: operations,
	}

	body, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}

	return string(body)
}

func createNestedDeploymentOperation(
	id string,
	deploymentName string,
	state armresources.ProvisioningState,
) *armresources.DeploymentOperation {
	resourceID := fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/"+
		"Microsoft.Resources/deployments/%s", deploymentName)

	return &armresources.DeploymentOperation{
		ID: new(id),
		Properties: &armresources.DeploymentOperationProperties{
			ProvisioningOperation: to.Ptr(armresources.ProvisioningOperationCreate),
			ProvisioningState:     new(string(state)),
			TargetResource: &armresources.TargetResource{
				ResourceType: to.Ptr(string(azapi.AzureResourceTypeDeployment)),
				ID:           new(resourceID),
				ResourceName: new(deploymentName),
			},
			Timestamp: new(time.Now().UTC().Add(time.Hour)),
		},
	}
}

func createLeafOperation(id string, resourceType string, resourceName string) *armresources.DeploymentOperation {
	resourceID := fmt.Sprintf(
		"/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/%s/%s",
		resourceType,
		resourceName,
	)

	return &armresources.DeploymentOperation{
		ID: new(id),
		Properties: &armresources.DeploymentOperationProperties{
			ProvisioningOperation: to.Ptr(armresources.ProvisioningOperationCreate),
			ProvisioningState:     to.Ptr(string(armresources.ProvisioningStateSucceeded)),
			TargetResource: &armresources.TargetResource{
				ResourceType: new(resourceType),
				ID:           new(resourceID),
				ResourceName: new(resourceName),
			},
			Timestamp: new(time.Now().UTC().Add(time.Hour)),
		},
	}
}

func mockDeploymentOperationsByName(
	t *testing.T,
	m mocks.MockContext,
	operationsByName map[string][]*armresources.DeploymentOperation,
) map[string]int {
	t.Helper()

	callCounts := map[string]int{}
	var callCountsMu sync.Mutex
	deploymentPathPattern := regexp.MustCompile(`(?i)/deployments/([^/]+)/operations`)

	m.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && deploymentPathPattern.MatchString(request.URL.Path)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		matches := deploymentPathPattern.FindStringSubmatch(request.URL.Path)
		deploymentName := ""
		if len(matches) > 1 {
			deploymentName = strings.ToLower(matches[1])
		}

		callCountsMu.Lock()
		callCounts[deploymentName]++
		callCountsMu.Unlock()

		operations, exists := operationsByName[deploymentName]
		if !exists {
			operations = []*armresources.DeploymentOperation{}
		}

		body := createDeploymentOperationResponse(t, operations...)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(body))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})

	return callCounts
}

func mockDeploymentOperationsByNameWithGate(
	t *testing.T,
	m mocks.MockContext,
	operationsByName map[string][]*armresources.DeploymentOperation,
	release <-chan struct{},
) {
	t.Helper()

	deploymentPathPattern := regexp.MustCompile(`(?i)/deployments/([^/]+)/operations`)

	m.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && deploymentPathPattern.MatchString(request.URL.Path)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		matches := deploymentPathPattern.FindStringSubmatch(request.URL.Path)
		deploymentName := ""
		if len(matches) > 1 {
			deploymentName = strings.ToLower(matches[1])
		}

		// For non-root deployments, wait for the gate to open.
		if deploymentName != "deployment_name" {
			<-release
		}

		operations, exists := operationsByName[deploymentName]
		if !exists {
			operations = []*armresources.DeploymentOperation{}
		}

		body := createDeploymentOperationResponse(t, operations...)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(body))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})
}

func TestWalkDeploymentOperationsSuccess(t *testing.T) {
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
	operationCount := 0
	err := arm.WalkDeploymentOperations(*mockContext.Context, deployment,
		func(ctx context.Context, operation *armresources.DeploymentOperation) error {
			operationCount++
			return nil
		})
	require.NoError(t, err)

	require.Equal(t, 5, operationCount)
	require.Equal(t, 1, subCalls)
	require.Equal(t, 1, groupCalls)
}

func TestWalkDeploymentOperationsSkipExpand(t *testing.T) {
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

	arm := NewAzureResourceManager(resourceService, deploymentService)
	err := arm.WalkDeploymentOperations(*mockContext.Context, deployment,
		func(ctx context.Context, operation *armresources.DeploymentOperation) error {
			if operation.ID != nil && *operation.ID == "deployment-id" {
				return SkipExpand()
			}

			return nil
		})
	require.NoError(t, err)
	require.Equal(t, 1, subCalls)
	require.Equal(t, 0, groupCalls)
}

func TestWalkDeploymentOperationsTreeShapes(t *testing.T) {
	tests := []struct {
		name                string
		operationsByName    map[string][]*armresources.DeploymentOperation
		expectedNestedCalls []string
	}{
		{
			name: "deep-chain",
			operationsByName: map[string][]*armresources.DeploymentOperation{
				"deployment_name": {
					createNestedDeploymentOperation("nested-1", "dep1", armresources.ProvisioningStateRunning),
				},
				"dep1": {
					createNestedDeploymentOperation("nested-2", "dep2", armresources.ProvisioningStateRunning),
					createLeafOperation("leaf-dep1", "Microsoft.Storage/storageAccounts", "storage-dep1"),
				},
				"dep2": {
					createNestedDeploymentOperation("nested-3", "dep3", armresources.ProvisioningStateRunning),
				},
				"dep3": {
					createLeafOperation("leaf-dep3", "Microsoft.KeyVault/vaults", "vault-dep3"),
				},
			},
			expectedNestedCalls: []string{"dep1", "dep2", "dep3"},
		},
		{
			name: "mixed-tree",
			operationsByName: map[string][]*armresources.DeploymentOperation{
				"deployment_name": {
					createNestedDeploymentOperation("nested-a", "depa", armresources.ProvisioningStateRunning),
					createNestedDeploymentOperation("nested-b", "depb", armresources.ProvisioningStateRunning),
					createLeafOperation("root-leaf", "Microsoft.Web/sites", "root-web"),
				},
				"depa": {
					createNestedDeploymentOperation("nested-a1", "depa1", armresources.ProvisioningStateRunning),
					createLeafOperation("depa-leaf", "Microsoft.Storage/storageAccounts", "depa-storage"),
				},
				"depa1": {
					createLeafOperation("depa1-leaf", "Microsoft.KeyVault/vaults", "depa1-vault"),
				},
				"depb": {
					createLeafOperation("depb-leaf", "Microsoft.Web/sites", "depb-web"),
				},
			},
			expectedNestedCalls: []string{"depa", "depa1", "depb"},
		},
		{
			name: "wide-fan-out",
			operationsByName: func() map[string][]*armresources.DeploymentOperation {
				operationsByName := map[string][]*armresources.DeploymentOperation{
					"deployment_name": {},
				}

				for i := range 12 {
					name := fmt.Sprintf("depw%d", i)
					operationsByName["deployment_name"] = append(
						operationsByName["deployment_name"],
						createNestedDeploymentOperation(
							fmt.Sprintf("nested-%s", name),
							name,
							armresources.ProvisioningStateRunning,
						),
					)
					operationsByName[name] = []*armresources.DeploymentOperation{
						createLeafOperation(
							fmt.Sprintf("leaf-%s", name),
							"Microsoft.Web/sites",
							fmt.Sprintf("site-%s", name),
						),
					}
				}

				return operationsByName
			}(),
			expectedNestedCalls: []string{
				"depw0", "depw1", "depw2", "depw3", "depw4", "depw5",
				"depw6", "depw7", "depw8", "depw9", "depw10", "depw11",
			},
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
			scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
			deployment := NewSubscriptionDeployment(scope, "DEPLOYMENT_NAME")

			callCounts := mockDeploymentOperationsByName(t, *mockContext, tt.operationsByName)

			arm := NewAzureResourceManager(resourceService, deploymentService)
			walkCount := 0
			err := arm.WalkDeploymentOperations(*mockContext.Context, deployment,
				func(ctx context.Context, operation *armresources.DeploymentOperation) error {
					walkCount++
					return nil
				})
			require.NoError(t, err)
			require.NotZero(t, walkCount)
			require.Equal(t, 1, callCounts["deployment_name"])

			for _, nestedDeploymentName := range tt.expectedNestedCalls {
				require.Equal(t, 1, callCounts[nestedDeploymentName])
			}
		})
	}
}

func TestWalkDeploymentOperationsContextCancelledDuringTraversal(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)
	scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
	deployment := NewSubscriptionDeployment(scope, "DEPLOYMENT_NAME")

	startedNestedFetch := make(chan struct{})
	releaseNestedFetch := make(chan struct{})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			strings.ToLower(request.URL.Path),
			"/providers/microsoft.resources/deployments/deployment_name/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := createDeploymentOperationResponse(t,
			createNestedDeploymentOperation("nested-cancel", "dep-cancel", armresources.ProvisioningStateRunning),
		)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(body))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			strings.ToLower(request.URL.Path),
			"/deployments/dep-cancel/operations",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		select {
		case <-startedNestedFetch:
		default:
			close(startedNestedFetch)
		}

		<-releaseNestedFetch

		body := createDeploymentOperationResponse(t)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte(body))),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    request.URL,
			},
		}, nil
	})

	ctx, cancel := context.WithCancel(*mockContext.Context)
	defer cancel()

	arm := NewAzureResourceManager(resourceService, deploymentService)
	resultCh := make(chan error, 1)

	go func() {
		err := arm.WalkDeploymentOperations(ctx, deployment,
			func(ctx context.Context, operation *armresources.DeploymentOperation) error {
				return nil
			})
		resultCh <- err
	}()

	select {
	case <-startedNestedFetch:
	case <-time.After(2 * time.Second):
		t.Fatal("nested deployment fetch did not start")
	}

	cancel()
	close(releaseNestedFetch)

	select {
	case err := <-resultCh:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("WalkDeploymentOperations did not return after cancellation")
	}
}

func TestWalkDeploymentOperationsCallbackErrorNoDeadlock(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)
	scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
	deployment := NewSubscriptionDeployment(scope, "DEPLOYMENT_NAME")

	// Root has two nested deployments so both workers will be busy.
	// Each nested deployment returns a leaf operation, giving the callback a chance to error.
	operationsByName := map[string][]*armresources.DeploymentOperation{
		"deployment_name": {
			createNestedDeploymentOperation("nested-a", "dep-a", armresources.ProvisioningStateRunning),
			createNestedDeploymentOperation("nested-b", "dep-b", armresources.ProvisioningStateRunning),
		},
		"dep-a": {
			createLeafOperation("leaf-a", "Microsoft.Web/sites", "site-a"),
		},
		"dep-b": {
			createLeafOperation("leaf-b", "Microsoft.Storage/storageAccounts", "storage-b"),
		},
	}

	// Gate nested fetches so both workers have results ready close together.
	release := make(chan struct{})
	mockDeploymentOperationsByNameWithGate(t, *mockContext, operationsByName, release)

	callbackErr := fmt.Errorf("callback failed")
	arm := NewAzureResourceManager(resourceService, deploymentService)

	resultCh := make(chan error, 1)
	go func() {
		callCount := 0
		err := arm.WalkDeploymentOperations(*mockContext.Context, deployment,
			func(ctx context.Context, operation *armresources.DeploymentOperation) error {
				// Error on the first nested leaf operation received, while the other worker
				// may be trying to send on the results channel.
				if operation.Properties != nil && operation.Properties.TargetResource != nil &&
					*operation.Properties.TargetResource.ResourceType != string(azapi.AzureResourceTypeDeployment) {
					callCount++
					if callCount == 1 {
						return callbackErr
					}
				}
				return nil
			})
		resultCh <- err
	}()

	// Release the gate so both nested fetches complete.
	close(release)

	select {
	case err := <-resultCh:
		require.ErrorIs(t, err, callbackErr)
	case <-time.After(5 * time.Second):
		t.Fatal("WalkDeploymentOperations deadlocked on callback error")
	}
}

func TestWalkDeploymentOperationsFail(t *testing.T) {
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
	err := arm.WalkDeploymentOperations(*mockContext.Context, deployment,
		func(ctx context.Context, operation *armresources.DeploymentOperation) error {
			return nil
		})
	require.NotNil(t, err)
	require.ErrorContains(t, err, "failed getting list of deployment operations from subscription")
	require.Equal(t, 1, subCalls)
	require.Equal(t, 0, groupCalls)
}

func TestWalkDeploymentOperationsNoResourceGroup(t *testing.T) {
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
	err := arm.WalkDeploymentOperations(*mockContext.Context, deployment,
		func(ctx context.Context, operation *armresources.DeploymentOperation) error {
			return nil
		})
	require.NoError(t, err)
	require.Equal(t, 1, subCalls)
	require.Equal(t, 0, groupCalls)
}

func TestWalkDeploymentOperationsWithNestedDeployments(t *testing.T) {
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
	operationCount := 0
	err := arm.WalkDeploymentOperations(*mockContext.Context, deployment,
		func(ctx context.Context, operation *armresources.DeploymentOperation) error {
			operationCount++
			return nil
		})

	require.NoError(t, err)
	require.Equal(t, 8, operationCount)
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
				ID:       new(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", SUBSCRIPTION_ID, name)),
				Type:     new("Microsoft.Resources/resourceGroups"),
				Name:     new(name),
				Location: new("eastus2"),
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

func TestGetResourceTypeDisplayNameForCognitiveServices(t *testing.T) {
	const SUBSCRIPTION_ID = "273f1e6b-6c19-4c9e-8b67-5fbe78b14063"

	tests := []struct {
		name         string
		resourceId   string
		resourceType azapi.AzureResourceType
		kind         string
		expectedName string
	}{
		{
			name: "Azure OpenAI",
			resourceId: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/test-rg/providers/Microsoft.CognitiveServices/accounts/test-openai",
				SUBSCRIPTION_ID,
			),
			resourceType: azapi.AzureResourceTypeCognitiveServiceAccount,
			kind:         "OpenAI",
			expectedName: "Azure OpenAI",
		},
		{
			name: "Document Intelligence (FormRecognizer)",
			resourceId: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/test-rg/providers/"+
					"Microsoft.CognitiveServices/accounts/test-formrecognizer",
				SUBSCRIPTION_ID,
			),
			resourceType: azapi.AzureResourceTypeCognitiveServiceAccount,
			kind:         "FormRecognizer",
			expectedName: "Document Intelligence",
		},
		{
			name: "Foundry (AIServices)",
			resourceId: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/test-rg/providers/Microsoft.CognitiveServices/accounts/test-foundry",
				SUBSCRIPTION_ID,
			),
			resourceType: azapi.AzureResourceTypeCognitiveServiceAccount,
			kind:         "AIServices",
			expectedName: "Foundry",
		},
		{
			name: "Foundry (AIHub)",
			resourceId: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/test-rg/providers/Microsoft.CognitiveServices/accounts/test-aihub",
				SUBSCRIPTION_ID,
			),
			resourceType: azapi.AzureResourceTypeCognitiveServiceAccount,
			kind:         "AIHub",
			expectedName: "Foundry",
		},
		{
			name: "Foundry project",
			resourceId: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/test-rg/providers/"+
					"Microsoft.CognitiveServices/accounts/test-foundry/projects/test-project",
				SUBSCRIPTION_ID,
			),
			resourceType: azapi.AzureResourceTypeCognitiveServiceAccountProject,
			kind:         "AIServices",
			expectedName: "Foundry project",
		},
		{
			name: "Azure AI Services (CognitiveServices)",
			resourceId: fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/test-rg/providers/Microsoft.CognitiveServices/accounts/test-cogservices",
				SUBSCRIPTION_ID,
			),
			resourceType: azapi.AzureResourceTypeCognitiveServiceAccount,
			kind:         "CognitiveServices",
			expectedName: "Azure AI Services",
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
				return request.Method == http.MethodGet &&
					strings.Contains(request.URL.Path, "/Microsoft.CognitiveServices/accounts/")
			}).RespondFn(func(request *http.Request) (*http.Response, error) {
				response := map[string]any{
					"id":       tt.resourceId,
					"name":     "test-resource",
					"type":     "Microsoft.CognitiveServices/accounts",
					"location": "eastus2",
					"kind":     tt.kind,
				}

				body, err := json.Marshal(response)
				if err != nil {
					return nil, err
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(body)),
					Request: &http.Request{
						Method: http.MethodGet,
						URL:    request.URL,
					},
				}, nil
			})

			arm := NewAzureResourceManager(resourceService, deploymentService)
			displayName, err := arm.GetResourceTypeDisplayName(
				*mockContext.Context,
				SUBSCRIPTION_ID,
				tt.resourceId,
				tt.resourceType,
			)

			require.NoError(t, err)
			require.Equal(t, tt.expectedName, displayName)
		})
	}
}

func TestGetResourceTypeDisplayNameForRedisEnterprise(t *testing.T) {
	const SUBSCRIPTION_ID = "273f1e6b-6c19-4c9e-8b67-5fbe78b14063"

	tests := []struct {
		name         string
		kind         string
		expectedName string
	}{
		{
			name:         "Azure Managed Redis (v2)",
			kind:         "v2",
			expectedName: "Azure Managed Redis",
		},
		{
			name:         "Redis Enterprise (v1)",
			kind:         "v1",
			expectedName: "Redis Enterprise",
		},
		{
			name:         "Redis Enterprise (default/empty)",
			kind:         "",
			expectedName: "Redis Enterprise",
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

			resourceId := fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/test-rg/providers/Microsoft.Cache/redisEnterprise/test-redis",
				SUBSCRIPTION_ID,
			)

			mockContext.HttpClient.When(func(request *http.Request) bool {
				return request.Method == http.MethodGet &&
					strings.Contains(request.URL.Path, "/Microsoft.Cache/redisEnterprise/test-redis")
			}).RespondFn(func(request *http.Request) (*http.Response, error) {
				response := map[string]any{
					"id":       resourceId,
					"name":     "test-redis",
					"type":     "Microsoft.Cache/redisEnterprise",
					"location": "eastus2",
					"kind":     tt.kind,
				}

				body, err := json.Marshal(response)
				if err != nil {
					return nil, err
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(body)),
					Request: &http.Request{
						Method: http.MethodGet,
						URL:    request.URL,
					},
				}, nil
			})

			arm := NewAzureResourceManager(resourceService, deploymentService)
			displayName, err := arm.GetResourceTypeDisplayName(
				*mockContext.Context,
				SUBSCRIPTION_ID,
				resourceId,
				azapi.AzureResourceTypeRedisEnterprise,
			)

			require.NoError(t, err)
			require.Equal(t, tt.expectedName, displayName)
		})
	}
}
