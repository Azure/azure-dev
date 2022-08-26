package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/httpUtil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/helpers"
	"github.com/stretchr/testify/require"
)

var gblCmdOptions = &commands.GlobalCommandOptions{
	EnableDebugLogging: false,
	EnableTelemetry:    true,
}

var mockSubDeploymentOperations = []azcli.AzCliResourceOperation{
	{
		Id: "resource-group-id",
		Properties: azcli.AzCliResourceOperationProperties{
			TargetResource: azcli.AzCliResourceOperationTargetResource{
				ResourceType:  string(AzureResourceTypeResourceGroup),
				Id:            "resource-group-id",
				ResourceName:  "resource-group-name",
				ResourceGroup: "resource-group-name",
			},
		},
	},
	{
		Id: "deployment-id",
		Properties: azcli.AzCliResourceOperationProperties{
			TargetResource: azcli.AzCliResourceOperationTargetResource{
				ResourceType:  string(AzureResourceTypeDeployment),
				Id:            "group-deployment-id",
				ResourceName:  "group-deployment-name",
				ResourceGroup: "group-deployment-name",
			},
		},
	},
}

var mockGroupDeploymentOperations = []azcli.AzCliResourceOperation{
	{
		Id: "website-resource-id",
		Properties: azcli.AzCliResourceOperationProperties{
			ProvisioningOperation: "Create",
			TargetResource: azcli.AzCliResourceOperationTargetResource{
				ResourceType:  string(AzureResourceTypeWebSite),
				Id:            "website-resource-id",
				ResourceName:  "website-resource-name",
				ResourceGroup: "resource-group-name",
			},
		},
	},
	{
		Id: "storage-resource-id",
		Properties: azcli.AzCliResourceOperationProperties{
			ProvisioningOperation: "Create",
			TargetResource: azcli.AzCliResourceOperationTargetResource{
				ResourceType:  string(AzureResourceTypeStorageAccount),
				Id:            "storage-resource-id",
				ResourceName:  "storage-resource-name",
				ResourceGroup: "resource-group-name",
			},
		},
	},
}
var mockNestedGroupDeploymentOperations = []azcli.AzCliResourceOperation{
	{
		Id: "website-resource-id",
		Properties: azcli.AzCliResourceOperationProperties{
			ProvisioningOperation: "Create",
			TargetResource: azcli.AzCliResourceOperationTargetResource{
				ResourceType:  string(AzureResourceTypeWebSite),
				Id:            "website-resource-id",
				ResourceName:  "website-resource-name",
				ResourceGroup: "resource-group-name",
			},
		},
	},
	{
		Id: "storage-resource-id",
		Properties: azcli.AzCliResourceOperationProperties{
			ProvisioningOperation: "Create",
			TargetResource: azcli.AzCliResourceOperationTargetResource{
				ResourceType:  string(AzureResourceTypeStorageAccount),
				Id:            "storage-resource-id",
				ResourceName:  "storage-resource-name",
				ResourceGroup: "resource-group-name",
			},
		},
	},
	{
		Id: "nested-deployment-id",
		Properties: azcli.AzCliResourceOperationProperties{
			TargetResource: azcli.AzCliResourceOperationTargetResource{
				ResourceType:  string(AzureResourceTypeDeployment),
				Id:            "nested-group-deployment-id",
				ResourceName:  "nested-group-deployment-name",
				ResourceGroup: "group-deployment-name",
			},
		},
	},
}

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

func TestGetDeploymentResourceOperationsSuccess(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	execFunc := func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
		if helpers.CallStackContains("ListSubscriptionDeploymentOperations") {
			subCalls++

			subJsonBytes, _ := json.Marshal(mockSubDeploymentOperations)
			return executil.NewRunResult(0, string(subJsonBytes), ""), nil
		}

		if helpers.CallStackContains("ListResourceGroupDeploymentOperations") {
			groupCalls++

			groupJsonBytes, _ := json.Marshal(mockGroupDeploymentOperations)
			return executil.NewRunResult(0, string(groupJsonBytes), ""), nil
		}

		return executil.NewRunResult(0, "", ""), nil
	}

	azCli := createTestAzCli(execFunc)
	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)

	arm := NewAzureResourceManager(azCli)
	operations, err := arm.GetDeploymentResourceOperations(ctx, "subscription-id", "deployment-name")
	require.NotNil(t, operations)
	require.Nil(t, err)

	require.Len(t, operations, 2)
	require.Equal(t, 1, subCalls)
	require.Equal(t, 1, groupCalls)
}

func TestGetDeploymentResourceOperationsFail(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	execFunc := func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
		if helpers.CallStackContains("ListSubscriptionDeploymentOperations") {
			subCalls++
			return executil.NewRunResult(1, "", "error getting resource operations"), nil
		}

		if helpers.CallStackContains("ListResourceGroupDeploymentOperations") {
			groupCalls++
			return executil.NewRunResult(0, "[]", ""), nil
		}

		return executil.RunResult{}, errors.New("No matching mock found")
	}

	azCli := createTestAzCli(execFunc)
	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)

	arm := NewAzureResourceManager(azCli)
	operations, err := arm.GetDeploymentResourceOperations(ctx, "subscription-id", "deployment-name")

	require.Nil(t, operations)
	require.NotNil(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "getting subscription deployment"))
	require.Equal(t, 1, subCalls)
	require.Equal(t, 0, groupCalls)
}

func TestGetDeploymentResourceOperationsNoResourceGroup(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	execFunc := func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
		if helpers.CallStackContains("ListSubscriptionDeploymentOperations") {
			subCalls++
			return executil.NewRunResult(0, "[]", ""), nil
		}

		if helpers.CallStackContains("ListResourceGroupDeploymentOperations") {
			groupCalls++
			return executil.NewRunResult(0, "[]", ""), nil
		}

		return executil.RunResult{}, errors.New("No matching mock found")
	}

	azCli := createTestAzCli(execFunc)
	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)

	arm := NewAzureResourceManager(azCli)
	operations, err := arm.GetDeploymentResourceOperations(ctx, "subscription-id", "deployment-name")

	require.NotNil(t, operations)
	require.Nil(t, err)
	require.Len(t, operations, 0)
	require.Equal(t, 1, subCalls)
	require.Equal(t, 0, groupCalls)
}

func TestGetDeploymentResourceOperationsWithNestedDeployments(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	execFunc := func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
		if helpers.CallStackContains("ListSubscriptionDeploymentOperations") {
			subCalls++

			subJsonBytes, _ := json.Marshal(mockSubDeploymentOperations)
			return executil.NewRunResult(0, string(subJsonBytes), ""), nil
		}

		if helpers.CallStackContains("ListResourceGroupDeploymentOperations") {
			groupCalls++

			if groupCalls == 1 {
				nestedGroupJsonBytes, _ := json.Marshal(mockNestedGroupDeploymentOperations)
				return executil.NewRunResult(0, string(nestedGroupJsonBytes), ""), nil
			} else {
				groupJsonBytes, _ := json.Marshal(mockGroupDeploymentOperations)
				return executil.NewRunResult(0, string(groupJsonBytes), ""), nil
			}
		}

		return executil.RunResult{}, errors.New("No matching mock found")
	}

	azCli := createTestAzCli(execFunc)
	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)

	arm := NewAzureResourceManager(azCli)
	operations, err := arm.GetDeploymentResourceOperations(ctx, "subscription-id", "deployment-name")

	require.NotNil(t, operations)
	require.Nil(t, err)
	require.Len(t, operations, 4)
	require.Equal(t, 1, subCalls)
	require.Equal(t, 2, groupCalls)
}

func createTestAzCli(execFunc func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error)) azcli.AzCli {
	return azcli.NewAzCli(azcli.NewAzCliArgs{
		EnableDebug:     false,
		EnableTelemetry: true,
		RunWithResultFn: execFunc,
	})
}
