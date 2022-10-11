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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockResourceManager struct {
	operations []azcli.AzCliResourceOperation
}

func (mock *mockResourceManager) GetDeploymentResourceOperations(ctx context.Context, scope infra.Scope) ([]azcli.AzCliResourceOperation, error) {
	return mock.operations, nil
}

func (mock *mockResourceManager) GetResourceTypeDisplayName(ctx context.Context, subscriptionId string, resourceId string, resourceType infra.AzureResourceType) (string, error) {
	return string(resourceType), nil
}

func (mock *mockResourceManager) GetWebAppResourceTypeDisplayName(ctx context.Context, subscriptionId string, resourceId string) (string, error) {
	return "", nil
}

func (mock *mockResourceManager) AddInProgressSubResourceOperation() {
	mock.operations = append(mock.operations, azcli.AzCliResourceOperation{Id: "website-deploy-id",
		Properties: azcli.AzCliResourceOperationProperties{
			ProvisioningOperation: "Create",
			TargetResource: azcli.AzCliResourceOperationTargetResource{
				ResourceType:  string(infra.AzureResourceTypeWebSite) + "/config",
				Id:            fmt.Sprintf("website-resource-id-%d", len(mock.operations)),
				ResourceName:  fmt.Sprintf("website-resource-name-%d", len(mock.operations)),
				ResourceGroup: "resource-group-name",
			},
		}})
}

func (mock *mockResourceManager) AddInProgressOperation() {
	mock.operations = append(mock.operations, azcli.AzCliResourceOperation{Id: "website-deploy-id",
		Properties: azcli.AzCliResourceOperationProperties{
			ProvisioningOperation: "Create",
			TargetResource: azcli.AzCliResourceOperationTargetResource{
				ResourceType:  string(infra.AzureResourceTypeWebSite),
				Id:            fmt.Sprintf("website-resource-id-%d", len(mock.operations)),
				ResourceName:  fmt.Sprintf("website-resource-name-%d", len(mock.operations)),
				ResourceGroup: "resource-group-name",
			},
		}})
}

func (mock *mockResourceManager) MarkComplete(i int) {
	mock.operations[i].Properties.ProvisioningState = succeededProvisioningState
	mock.operations[i].Properties.Timestamp = time.Now().UTC()
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
		}, nil
	})
}

func TestReportProgress(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	scope := infra.NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")
	mockAzDeploymentShow(t, *mockContext)

	outputLength := 0
	mockResourceManager := mockResourceManager{}
	progressDisplay := NewProvisioningProgressDisplay(&mockResourceManager, mockContext.Console, scope)
	progressReport, _ := progressDisplay.ReportProgress(*mockContext.Context)
	outputLength++
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assert.Contains(t, mockContext.Console.Output()[0], deploymentStartedDisplayMessage)
	assert.Equal(t, defaultProgressTitle, progressReport.Message)

	mockResourceManager.AddInProgressOperation()
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assert.Equal(t, formatProgressTitle(0, 1), progressReport.Message)

	mockResourceManager.AddInProgressOperation()
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assert.Equal(t, formatProgressTitle(0, 2), progressReport.Message)

	mockResourceManager.AddInProgressSubResourceOperation()
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assert.Equal(t, formatProgressTitle(0, 3), progressReport.Message)

	mockResourceManager.MarkComplete(0)
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	outputLength++
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assertLastOperationLogged(t, mockResourceManager.operations[0], mockContext.Console.Output())
	assert.Equal(t, formatProgressTitle(1, 3), progressReport.Message)

	mockResourceManager.MarkComplete(1)
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	outputLength++
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assertLastOperationLogged(t, mockResourceManager.operations[1], mockContext.Console.Output())
	assert.Equal(t, formatProgressTitle(2, 3), progressReport.Message)

	// Verify display does not log sub resource types
	mockResourceManager.MarkComplete(2)
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assert.Equal(t, formatProgressTitle(3, 3), progressReport.Message)

	// Verify display does not repeat logging for resources already logged.
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Len(t, mockContext.Console.Output(), outputLength)
	assert.Equal(t, formatProgressTitle(3, 3), progressReport.Message)
}

func assertLastOperationLogged(t *testing.T, operation azcli.AzCliResourceOperation, logOutput []string) {
	assert.Equal(t, formatCreatedResourceLog(operation.Properties.TargetResource.ResourceType, operation.Properties.TargetResource.ResourceName), logOutput[len(logOutput)-1])
}
