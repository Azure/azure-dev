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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockResourceManager struct {
	operations []*armresources.DeploymentOperation
}

func (mock *mockResourceManager) WalkDeploymentOperations(
	ctx context.Context,
	target Deployment,
	fn WalkDeploymentOperationFunc,
) error {
	for _, operation := range mock.operations {
		if err := fn(ctx, operation); err != nil && !IsSkipExpand(err) {
			return err
		}
	}

	return nil
}

func (mock *mockResourceManager) GetResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
	resourceType azapi.AzureResourceType,
) (string, error) {
	return string(resourceType), nil
}

func (mock *mockResourceManager) GetResourceGroupsForEnvironment(
	ctx context.Context,
	subscriptionId string,
	envName string,
) ([]*azapi.Resource, error) {
	return []*azapi.Resource{}, nil
}

func (mock *mockResourceManager) FindResourceGroupForEnvironment(
	ctx context.Context,
	subscriptionId string,
	envName string,
) (string, error) {
	return fmt.Sprintf("rg-%s", envName), nil
}

func (mock *mockResourceManager) AddInProgressSubResourceOperation() {
	mock.operations = append(mock.operations, &armresources.DeploymentOperation{
		ID: new("website-deploy-id"),
		Properties: &armresources.DeploymentOperationProperties{
			ProvisioningOperation: to.Ptr(armresources.ProvisioningOperation("Create")),
			TargetResource: &armresources.TargetResource{
				ResourceType: to.Ptr(string(azapi.AzureResourceTypeWebSite) + "/config"),
				ID:           new(fmt.Sprintf("website-resource-id-%d", len(mock.operations))),
				ResourceName: new(fmt.Sprintf("website-resource-name-%d", len(mock.operations))),
			},
			ProvisioningState: new("In Progress"),
			Timestamp:         new(time.Now().UTC()),
		}})
}

func (mock *mockResourceManager) AddInProgressOperation() {
	mock.operations = append(mock.operations, &armresources.DeploymentOperation{
		ID: new("website-deploy-id"),
		Properties: &armresources.DeploymentOperationProperties{
			ProvisioningOperation: to.Ptr(armresources.ProvisioningOperation("Create")),
			TargetResource: &armresources.TargetResource{
				ResourceType: to.Ptr(string(azapi.AzureResourceTypeWebSite)),
				ID:           new(fmt.Sprintf("website-resource-id-%d", len(mock.operations))),
				ResourceName: new(fmt.Sprintf("website-resource-name-%d", len(mock.operations))),
			},
			ProvisioningState: new("In Progress"),
			Timestamp:         new(time.Now().UTC()),
		}})
}

func (mock *mockResourceManager) MarkComplete(i int) {
	mock.operations[i].Properties.ProvisioningState = to.Ptr(string(armresources.ProvisioningStateSucceeded))
	mock.operations[i].Properties.Timestamp = new(time.Now().UTC())
}

func mockAzDeploymentShow(t *testing.T, m mocks.MockContext) {
	deployment := armresources.DeploymentExtended{
		ID:       new("/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME"),
		Location: new("eastus2"),
		Properties: &armresources.DeploymentPropertiesExtended{
			ProvisioningState: to.Ptr(armresources.ProvisioningStateCreated),
			Timestamp:         new(time.Now().UTC()),
		},
		Tags: map[string]*string{},
		Name: new("DEPLOYMENT_NAME"),
		Type: new("Microsoft.Resources/deployments"),
	}
	deploymentJson, err := json.Marshal(deployment)
	require.NoError(t, err)
	m.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(deploymentJson)),
			Request: &http.Request{
				Method: http.MethodGet,
			},
		}, nil
	})
}

func TestReportProgress(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	deploymentService := mockazapi.NewDeploymentsServiceFromMockContext(mockContext)

	scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
	deployment := NewSubscriptionDeployment(
		scope,
		"DEPLOYMENT_NAME",
	)
	mockAzDeploymentShow(t, *mockContext)

	startTime := time.Now()
	outputLength := 0
	mockResourceManager := mockResourceManager{}
	progressDisplay := NewProvisioningProgressDisplay(&mockResourceManager, mockContext.Console, deployment)
	err := progressDisplay.ReportProgress(*mockContext.Context, &startTime)
	require.NoError(t, err)

	outputLength++
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assert.Contains(t, mockContext.Console.Output()[0], "You can view detailed progress in the Azure Portal:")

	mockResourceManager.AddInProgressOperation()
	err = progressDisplay.ReportProgress(*mockContext.Context, &startTime)
	require.NoError(t, err)
	assert.Len(t, mockContext.Console.Output(), outputLength)
}

type walkSkipAwareResourceManager struct {
	nestedOperation *armresources.DeploymentOperation
	childOperation  *armresources.DeploymentOperation
	childVisits     int
}

func (mock *walkSkipAwareResourceManager) WalkDeploymentOperations(
	ctx context.Context,
	target Deployment,
	fn WalkDeploymentOperationFunc,
) error {
	err := fn(ctx, mock.nestedOperation)
	if err != nil {
		if IsSkipExpand(err) {
			return nil
		}

		return err
	}

	mock.childVisits++
	return fn(ctx, mock.childOperation)
}

func (mock *walkSkipAwareResourceManager) GetResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
	resourceType azapi.AzureResourceType,
) (string, error) {
	return string(resourceType), nil
}

func (mock *walkSkipAwareResourceManager) GetResourceGroupsForEnvironment(
	ctx context.Context,
	subscriptionId string,
	envName string,
) ([]*azapi.Resource, error) {
	return []*azapi.Resource{}, nil
}

func (mock *walkSkipAwareResourceManager) FindResourceGroupForEnvironment(
	ctx context.Context,
	subscriptionId string,
	envName string,
) (string, error) {
	return "", nil
}

func TestReportProgressSkipsExpansionAfterTwoTerminalPolls(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	deploymentService := mockazapi.NewDeploymentsServiceFromMockContext(mockContext)

	scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
	deployment := NewSubscriptionDeployment(
		scope,
		"DEPLOYMENT_NAME",
	)
	mockAzDeploymentShow(t, *mockContext)

	walkRm := &walkSkipAwareResourceManager{
		nestedOperation: &armresources.DeploymentOperation{
			ID: new("nested-operation-id"),
			Properties: &armresources.DeploymentOperationProperties{
				ProvisioningOperation: to.Ptr(armresources.ProvisioningOperationCreate),
				ProvisioningState:     to.Ptr(string(armresources.ProvisioningStateSucceeded)),
				TargetResource: &armresources.TargetResource{
					ResourceType: to.Ptr(string(azapi.AzureResourceTypeDeployment)),
					ID: new("/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/" +
						"Microsoft.Resources/deployments/nested"),
					ResourceName: new("nested"),
				},
				Timestamp: new(time.Now().UTC()),
			},
		},
		childOperation: &armresources.DeploymentOperation{
			ID: new("child-operation-id"),
			Properties: &armresources.DeploymentOperationProperties{
				ProvisioningOperation: to.Ptr(armresources.ProvisioningOperationCreate),
				ProvisioningState:     to.Ptr(string(armresources.ProvisioningStateSucceeded)),
				Duration:              new("PT1S"),
				TargetResource: &armresources.TargetResource{
					ResourceType: to.Ptr(string(azapi.AzureResourceTypeWebSite)),
					ID: new("/subscriptions/SUBSCRIPTION_ID/resourceGroups/resource-group-name/providers/" +
						"Microsoft.Web/sites/site"),
					ResourceName: new("site"),
				},
				Timestamp: new(time.Now().UTC()),
			},
		},
	}

	progressDisplay := NewProvisioningProgressDisplay(walkRm, mockContext.Console, deployment)
	startTime := time.Now().Add(-time.Minute)

	err := progressDisplay.ReportProgress(*mockContext.Context, &startTime)
	require.NoError(t, err)
	err = progressDisplay.ReportProgress(*mockContext.Context, &startTime)
	require.NoError(t, err)

	require.Equal(t, 1, walkRm.childVisits)
}
