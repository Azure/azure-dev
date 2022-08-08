package provisioning

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/assert"
)

type mockResourceManager struct {
	operations []tools.AzCliResourceOperation
}

func (mock *mockResourceManager) GetDeploymentResourceOperations(ctx context.Context, subscriptionId string, deploymentName string) ([]tools.AzCliResourceOperation, error) {
	return mock.operations, nil
}

func (mock *mockResourceManager) GetResourceTypeDisplayName(ctx context.Context, subscriptionId string, resourceId string, resourceType infra.AzureResourceType) (string, error) {
	return string(resourceType), nil
}

func (mock *mockResourceManager) GetWebAppResourceTypeDisplayName(ctx context.Context, subscriptionId string, resourceId string) (string, error) {
	return "", nil
}

func (mock *mockResourceManager) AddInProgressSubResourceOperation() {
	mock.operations = append(mock.operations, tools.AzCliResourceOperation{Id: "website-deploy-id",
		Properties: tools.AzCliResourceOperationProperties{
			ProvisioningOperation: "Create",
			TargetResource: tools.AzCliResourceOperationTargetResource{
				ResourceType:  string(infra.AzureResourceTypeWebSite) + "/config",
				Id:            fmt.Sprintf("website-resource-id-%d", len(mock.operations)),
				ResourceName:  fmt.Sprintf("website-resource-name-%d", len(mock.operations)),
				ResourceGroup: "resource-group-name",
			},
		}})
}

func (mock *mockResourceManager) AddInProgressOperation() {
	mock.operations = append(mock.operations, tools.AzCliResourceOperation{Id: "website-deploy-id",
		Properties: tools.AzCliResourceOperationProperties{
			ProvisioningOperation: "Create",
			TargetResource: tools.AzCliResourceOperationTargetResource{
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
	t.Run("Displays progress correctly", func(t *testing.T) {
		mockResourceManager := mockResourceManager{}
		progressDisplay := NewProvisioningProgressDisplay(&mockResourceManager, "", "")
		logOutput := []string{}
		progressTitle := ""
		progressDisplay.reportProgress(&progressTitle, &logOutput)
		assert.Empty(t, logOutput)

		mockResourceManager.AddInProgressOperation()
		progressDisplay.reportProgress(&progressTitle, &logOutput)
		assert.Empty(t, logOutput)
		assert.Equal(t, formatProgressTitle(0, 1), progressTitle)

		mockResourceManager.AddInProgressOperation()
		progressDisplay.reportProgress(&progressTitle, &logOutput)
		assert.Empty(t, logOutput)
		assert.Equal(t, formatProgressTitle(0, 2), progressTitle)

		mockResourceManager.AddInProgressSubResourceOperation()
		progressDisplay.reportProgress(&progressTitle, &logOutput)
		assert.Empty(t, logOutput)
		assert.Equal(t, formatProgressTitle(0, 3), progressTitle)

		mockResourceManager.MarkComplete(0)
		progressDisplay.reportProgress(&progressTitle, &logOutput)
		assert.Len(t, logOutput, 1)
		assertOperationLogged(t, 0, mockResourceManager.operations, logOutput)
		assert.Equal(t, formatProgressTitle(1, 3), progressTitle)

		mockResourceManager.MarkComplete(1)
		progressDisplay.reportProgress(&progressTitle, &logOutput)
		assert.Len(t, logOutput, 2)
		assertOperationLogged(t, 1, mockResourceManager.operations, logOutput)
		assert.Equal(t, formatProgressTitle(2, 3), progressTitle)

		// Verify display does not log sub resource types
		oldLogOutput := make([]string, len(logOutput))
		copy(logOutput, oldLogOutput)
		mockResourceManager.MarkComplete(2)
		progressDisplay.reportProgress(&progressTitle, &logOutput)
		assert.Equal(t, oldLogOutput, logOutput)
		assert.Equal(t, formatProgressTitle(3, 3), progressTitle)

		// Verify display does not repeat logging for resources already logged.
		copy(logOutput, oldLogOutput)
		progressDisplay.reportProgress(&progressTitle, &logOutput)
		assert.Equal(t, oldLogOutput, logOutput)
		assert.Equal(t, formatProgressTitle(3, 3), progressTitle)
	})
}

func (display *ProvisioningProgressDisplay) reportProgress(captureTitle *string, captureLogOutput *[]string) {
	display.ReportProgress(context.Background(), titleCapturer(captureTitle), logOutputCapturer(captureLogOutput))
}

func assertOperationLogged(t *testing.T, i int, operations []tools.AzCliResourceOperation, logOutput []string) {
	assert.True(t, len(logOutput) > i)
	assert.Equal(t, formatCreatedResourceLog(operations[i].Properties.TargetResource.ResourceType, operations[i].Properties.TargetResource.ResourceName), logOutput[i])
}

func titleCapturer(title *string) func(string) {
	return func(s string) {
		*title = s
	}
}

func logOutputCapturer(logOutput *[]string) func(string) {
	return func(s string) {
		*logOutput = append(*logOutput, s)
	}
}
