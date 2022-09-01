// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
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

func TestReportProgress(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	scope := infra.NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

	mockResourceManager := mockResourceManager{}
	progressDisplay := NewProvisioningProgressDisplay(&mockResourceManager, mockContext.Console, scope)
	progressReport, _ := progressDisplay.ReportProgress(*mockContext.Context)
	assert.Empty(t, mockContext.Console.Output())
	assert.Equal(t, defaultProgressTitle, progressReport.Message)

	mockResourceManager.AddInProgressOperation()
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Empty(t, mockContext.Console.Output())
	assert.Equal(t, formatProgressTitle(0, 1), progressReport.Message)

	mockResourceManager.AddInProgressOperation()
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Empty(t, mockContext.Console.Output())
	assert.Equal(t, formatProgressTitle(0, 2), progressReport.Message)

	mockResourceManager.AddInProgressSubResourceOperation()
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Empty(t, mockContext.Console.Output())
	assert.Equal(t, formatProgressTitle(0, 3), progressReport.Message)

	mockResourceManager.MarkComplete(0)
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Len(t, mockContext.Console.Output(), 1)
	assertOperationLogged(t, 0, mockResourceManager.operations, mockContext.Console.Output())
	assert.Equal(t, formatProgressTitle(1, 3), progressReport.Message)

	mockResourceManager.MarkComplete(1)
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Len(t, mockContext.Console.Output(), 2)
	assertOperationLogged(t, 1, mockResourceManager.operations, mockContext.Console.Output())
	assert.Equal(t, formatProgressTitle(2, 3), progressReport.Message)

	// Verify display does not log sub resource types
	oldLogOutput := make([]string, len(mockContext.Console.Output()))
	copy(mockContext.Console.Output(), oldLogOutput)
	mockResourceManager.MarkComplete(2)
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Equal(t, oldLogOutput, mockContext.Console.Output())
	assert.Equal(t, formatProgressTitle(3, 3), progressReport.Message)

	// Verify display does not repeat logging for resources already logged.
	copy(mockContext.Console.Output(), oldLogOutput)
	progressReport, _ = progressDisplay.ReportProgress(*mockContext.Context)
	assert.Equal(t, oldLogOutput, mockContext.Console.Output())
	assert.Equal(t, formatProgressTitle(3, 3), progressReport.Message)
}

func assertOperationLogged(t *testing.T, i int, operations []azcli.AzCliResourceOperation, logOutput []string) {
	assert.True(t, len(logOutput) > i)
	assert.Equal(t, formatCreatedResourceLog(operations[i].Properties.TargetResource.ResourceType, operations[i].Properties.TargetResource.ResourceName), logOutput[i])
}
