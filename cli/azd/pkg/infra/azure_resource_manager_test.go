// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

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

func TestGetDeploymentResourceOperationsSuccess(t *testing.T) {
	subCalls := 0
	groupCalls := 0

	mockContext := mocks.NewMockContext(context.Background())
	scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment operation sub list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		subCalls++

		subJsonBytes, _ := json.Marshal(mockSubDeploymentOperations)
		return exec.NewRunResult(0, string(subJsonBytes), ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment operation group list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		groupCalls++

		groupJsonBytes, _ := json.Marshal(mockGroupDeploymentOperations)
		return exec.NewRunResult(0, string(groupJsonBytes), ""), nil
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

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment operation sub list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		subCalls++

		return exec.NewRunResult(1, "", "error getting resource operations"), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment operation group list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		groupCalls++

		return exec.NewRunResult(0, "[]", ""), nil
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

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment operation sub list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		subCalls++

		return exec.NewRunResult(0, "[]", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment operation group list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		groupCalls++

		return exec.NewRunResult(0, "[]", ""), nil
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

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment operation sub list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		subCalls++

		subJsonBytes, _ := json.Marshal(mockSubDeploymentOperations)
		return exec.NewRunResult(0, string(subJsonBytes), ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az deployment operation group list")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		groupCalls++

		if groupCalls == 1 {
			nestedGroupJsonBytes, _ := json.Marshal(mockNestedGroupDeploymentOperations)
			return exec.NewRunResult(0, string(nestedGroupJsonBytes), ""), nil
		} else {
			groupJsonBytes, _ := json.Marshal(mockGroupDeploymentOperations)
			return exec.NewRunResult(0, string(groupJsonBytes), ""), nil
		}
	})

	arm := NewAzureResourceManager(*mockContext.Context)
	operations, err := arm.GetDeploymentResourceOperations(*mockContext.Context, scope)

	require.NotNil(t, operations)
	require.Nil(t, err)
	require.Len(t, operations, 6)
	require.Equal(t, 1, subCalls)
	require.Equal(t, 2, groupCalls)
}
