// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

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
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockResourceManager struct {
	operations []*armresources.DeploymentOperation
}

func (mock *mockResourceManager) GetDeploymentResourceOperations(
	ctx context.Context,
	scope infra.Scope,
	startTime *time.Time,
) ([]*armresources.DeploymentOperation, error) {
	return mock.operations, nil
}

func (mock *mockResourceManager) GetResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
	resourceType infra.AzureResourceType,
) (string, error) {
	return string(resourceType), nil
}

func (mock *mockResourceManager) GetWebAppResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
) (string, error) {
	return "", nil
}

func (mock *mockResourceManager) AddInProgressSubResourceOperation() {
	mock.operations = append(mock.operations, &armresources.DeploymentOperation{
		ID: to.Ptr("website-deploy-id"),
		Properties: &armresources.DeploymentOperationProperties{
			ProvisioningOperation: to.Ptr(armresources.ProvisioningOperation("Create")),
			TargetResource: &armresources.TargetResource{
				ResourceType: to.Ptr(string(infra.AzureResourceTypeWebSite) + "/config"),
				ID:           to.Ptr(fmt.Sprintf("website-resource-id-%d", len(mock.operations))),
				ResourceName: to.Ptr(fmt.Sprintf("website-resource-name-%d", len(mock.operations))),
			},
			ProvisioningState: to.Ptr("In Progress"),
			Timestamp:         to.Ptr(time.Now().UTC()),
		}})
}

func (mock *mockResourceManager) AddInProgressOperation() {
	mock.operations = append(mock.operations, &armresources.DeploymentOperation{
		ID: to.Ptr("website-deploy-id"),
		Properties: &armresources.DeploymentOperationProperties{
			ProvisioningOperation: to.Ptr(armresources.ProvisioningOperation("Create")),
			TargetResource: &armresources.TargetResource{
				ResourceType: to.Ptr(string(infra.AzureResourceTypeWebSite)),
				ID:           to.Ptr(fmt.Sprintf("website-resource-id-%d", len(mock.operations))),
				ResourceName: to.Ptr(fmt.Sprintf("website-resource-name-%d", len(mock.operations))),
			},
			ProvisioningState: to.Ptr("In Progress"),
			Timestamp:         to.Ptr(time.Now().UTC()),
		}})
}

func (mock *mockResourceManager) MarkComplete(i int) {
	mock.operations[i].Properties.ProvisioningState = to.Ptr(succeededProvisioningState)
	mock.operations[i].Properties.Timestamp = to.Ptr(time.Now().UTC())
}

func mockAzDeploymentShow(t *testing.T, m mocks.MockContext) {
	deployment := armresources.DeploymentExtended{}
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
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	scope := infra.NewSubscriptionScope(azCli, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")
	mockAzDeploymentShow(t, *mockContext)

	startTime := time.Now()
	outputLength := 0
	mockResourceManager := mockResourceManager{}
	progressDisplay := NewProvisioningProgressDisplay(&mockResourceManager, mockContext.Console, scope)
	progressReport, _ := progressDisplay.ReportProgress(*mockContext.Context, &startTime)
	outputLength++
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assert.Contains(t, mockContext.Console.Output()[0], "You can view detailed progress in the Azure Portal:")
	assert.Equal(t, defaultProgressTitle, progressReport.Message)

	mockResourceManager.AddInProgressOperation()
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context, &startTime)
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assert.Equal(t, "Provisioning Azure resources", progressReport.Message)
}
