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
		{"twoTaggedResourceGroups", []string{"custom-rg-name", "custom-rg-name-2"}, nil, "", "more than one possible resource group was found"},
		{"noResourceGroups", nil, nil, "", "0 resource groups with prefix or suffix with value"},
		{"noTagMultipleMatches", nil, []string{"test-env-rg", "rg-test-env"}, "", "more than one possible resource group was found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			mockContext.HttpClient.When(func(request *http.Request) bool {
				return request.Method == "GET" && strings.Contains(request.URL.Path, "/resourcegroups") && request.URL.Query().Has("$filter")
			}).RespondFn(func(request *http.Request) (*http.Response, error) {
				require.Contains(t, request.URL.Query().Get("$filter"), "tagName eq 'azd-env-name'")
				require.Contains(t, request.URL.Query().Get("$filter"), "tagValue eq 'test-env'")

				return responseForGroups(tt.rgsFromFilter), nil
			})
			mockContext.HttpClient.When(func(request *http.Request) bool {
				return request.Method == "GET" && strings.Contains(request.URL.Path, "/resourcegroups") && !request.URL.Query().Has("$filter")
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
