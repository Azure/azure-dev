// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Tests for conversion helper functions. Additional test coverage for
// ProvisioningService (gRPC registration flow), ProvisioningManager (handler
// dispatch, multi-provider routing), and ProvisioningEnvelope (message
// marshaling) is tracked in GitHub issue #7480.

package grpcserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
)

func Test_convertToProtoOptions(t *testing.T) {
	tests := []struct {
		name    string
		options provisioning.Options
		verify  func(t *testing.T, result *azdext.ProvisioningOptions, err error)
	}{
		{
			name: "FullOptions",
			options: provisioning.Options{
				Provider: provisioning.Bicep,
				Path:     "/infra",
				Module:   "main",
				Name:     "layer1",
				DeploymentStacks: &provisioning.DeploymentStacksConfig{
					DenySettings: &provisioning.DenySettingsConfig{Mode: "denyDelete"},
				},
				IgnoreDeploymentState: true,
				Config: map[string]any{
					"key1": "value1",
					"nested": map[string]any{
						"key2": "value2",
					},
				},
			},
			verify: func(t *testing.T, result *azdext.ProvisioningOptions, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, "bicep", result.Provider)
				assert.Equal(t, "/infra", result.Path)
				assert.Equal(t, "main", result.Module)
				assert.Equal(t, "layer1", result.Name)
				assert.True(t, result.IgnoreDeploymentState)
				// deploymentStacks is bicep-only and intentionally not forwarded to extensions.
				assert.Empty(t, result.DeploymentStacks)
				require.NotNil(t, result.Config)
				assert.Equal(t, "value1", result.Config.Fields["key1"].GetStringValue())
				nested := result.Config.Fields["nested"].GetStructValue()
				require.NotNil(t, nested)
				assert.Equal(t, "value2", nested.Fields["key2"].GetStringValue())
			},
		},
		{
			name:    "EmptyOptions",
			options: provisioning.Options{},
			verify: func(t *testing.T, result *azdext.ProvisioningOptions, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Empty(t, result.Provider)
				assert.Empty(t, result.Path)
				assert.Empty(t, result.Module)
				assert.Empty(t, result.Name)
				assert.False(t, result.IgnoreDeploymentState)
				assert.Empty(t, result.DeploymentStacks)
				assert.Nil(t, result.Config)
			},
		},
		{
			name: "NilConfig",
			options: provisioning.Options{
				Provider: provisioning.Terraform,
				Path:     "/terraform",
			},
			verify: func(t *testing.T, result *azdext.ProvisioningOptions, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, "terraform", result.Provider)
				assert.Nil(t, result.Config)
			},
		},
		{
			name: "OptionsWithVirtualEnv",
			options: provisioning.Options{
				Provider: provisioning.Bicep,
				Path:     "infra",
				VirtualEnv: map[string]string{
					"LAYER1_OUTPUT":   "value1",
					"LAYER1_ENDPOINT": "https://example.com",
				},
			},
			verify: func(t *testing.T, result *azdext.ProvisioningOptions, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, "bicep", result.Provider)
				assert.Equal(t, "infra", result.Path)
				require.Len(t, result.VirtualEnv, 2)
				assert.Equal(t, "value1", result.VirtualEnv["LAYER1_OUTPUT"])
				assert.Equal(t, "https://example.com", result.VirtualEnv["LAYER1_ENDPOINT"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertToProtoOptions(tt.options)
			tt.verify(t, result, err)
		})
	}
}

func Test_convertFromProtoStateResult(t *testing.T) {
	tests := []struct {
		name   string
		result *azdext.ProvisioningStateResult
		verify func(t *testing.T, result *provisioning.StateResult)
	}{
		{
			name:   "Nil",
			result: nil,
			verify: func(t *testing.T, result *provisioning.StateResult) {
				require.NotNil(t, result)
				assert.Nil(t, result.State)
			},
		},
		{
			name:   "NilState",
			result: &azdext.ProvisioningStateResult{State: nil},
			verify: func(t *testing.T, result *provisioning.StateResult) {
				require.NotNil(t, result)
				assert.Nil(t, result.State)
			},
		},
		{
			name: "EmptyState",
			result: &azdext.ProvisioningStateResult{
				State: &azdext.ProvisioningState{},
			},
			verify: func(t *testing.T, result *provisioning.StateResult) {
				require.NotNil(t, result)
				require.NotNil(t, result.State)
				assert.Empty(t, result.State.Outputs)
				assert.Empty(t, result.State.Resources)
			},
		},
		{
			name: "PopulatedState",
			result: &azdext.ProvisioningStateResult{
				State: &azdext.ProvisioningState{
					Outputs: map[string]*azdext.ProvisioningOutputParameter{
						"ENDPOINT": {Type: "string", Value: "https://example.com"},
						"PORT":     {Type: "number", Value: "8080"},
					},
					Resources: []*azdext.ProvisioningResource{
						{Id: "/subscriptions/sub1/resourceGroups/rg1"},
						{Id: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Web/sites/app1"},
					},
				},
			},
			verify: func(t *testing.T, result *provisioning.StateResult) {
				require.NotNil(t, result)
				require.NotNil(t, result.State)
				assert.Len(t, result.State.Outputs, 2)

				endpoint := result.State.Outputs["ENDPOINT"]
				assert.Equal(t, provisioning.ParameterType("string"), endpoint.Type)
				assert.Equal(t, "https://example.com", endpoint.Value)

				port := result.State.Outputs["PORT"]
				assert.Equal(t, provisioning.ParameterType("number"), port.Type)
				assert.Equal(t, "8080", port.Value)

				assert.Len(t, result.State.Resources, 2)
				assert.Equal(t, "/subscriptions/sub1/resourceGroups/rg1", result.State.Resources[0].Id)
				assert.Equal(
					t,
					"/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Web/sites/app1",
					result.State.Resources[1].Id,
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertFromProtoStateResult(tt.result)
			tt.verify(t, result)
		})
	}
}

func Test_convertFromProtoDeployResult(t *testing.T) {
	tests := []struct {
		name   string
		result *azdext.ProvisioningDeployResult
		verify func(t *testing.T, result *provisioning.DeployResult)
	}{
		{
			name:   "EmptyResult",
			result: &azdext.ProvisioningDeployResult{},
			verify: func(t *testing.T, result *provisioning.DeployResult) {
				require.NotNil(t, result)
				assert.Nil(t, result.Deployment)
				assert.Empty(t, result.SkippedReason)
			},
		},
		{
			name: "FullDeployment",
			result: &azdext.ProvisioningDeployResult{
				Deployment: &azdext.ProvisioningDeployment{
					Parameters: map[string]*azdext.ProvisioningInputParameter{
						"location": {
							Type:         "string",
							DefaultValue: "eastus",
							Value:        "westus2",
						},
						"sku": {
							Type:  "string",
							Value: "B1",
						},
					},
					Outputs: map[string]*azdext.ProvisioningOutputParameter{
						"ENDPOINT": {Type: "string", Value: "https://app.example.com"},
					},
				},
			},
			verify: func(t *testing.T, result *provisioning.DeployResult) {
				require.NotNil(t, result)
				require.NotNil(t, result.Deployment)
				assert.Empty(t, result.SkippedReason)

				location := result.Deployment.Parameters["location"]
				assert.Equal(t, "string", location.Type)
				assert.Equal(t, "eastus", location.DefaultValue)
				assert.Equal(t, "westus2", location.Value)

				sku := result.Deployment.Parameters["sku"]
				assert.Equal(t, "string", sku.Type)
				assert.Nil(t, sku.DefaultValue)
				assert.Equal(t, "B1", sku.Value)

				endpoint := result.Deployment.Outputs["ENDPOINT"]
				assert.Equal(t, provisioning.ParameterType("string"), endpoint.Type)
				assert.Equal(t, "https://app.example.com", endpoint.Value)
			},
		},
		{
			name: "SkippedDeploymentState",
			result: &azdext.ProvisioningDeployResult{
				SkippedReason: azdext.ProvisioningSkippedReason_PROVISIONING_SKIPPED_REASON_DEPLOYMENT_STATE,
			},
			verify: func(t *testing.T, result *provisioning.DeployResult) {
				require.NotNil(t, result)
				assert.Equal(t, provisioning.DeploymentStateSkipped, result.SkippedReason)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertFromProtoDeployResult(tt.result)
			tt.verify(t, result)
		})
	}
}

func Test_convertFromProtoPreviewResult(t *testing.T) {
	tests := []struct {
		name   string
		result *azdext.ProvisioningPreviewResult
		verify func(t *testing.T, result *provisioning.DeployPreviewResult)
	}{
		{
			name:   "Nil",
			result: nil,
			verify: func(t *testing.T, result *provisioning.DeployPreviewResult) {
				require.NotNil(t, result)
				assert.Nil(t, result.Preview)
			},
		},
		{
			name:   "NilPreview",
			result: &azdext.ProvisioningPreviewResult{Preview: nil},
			verify: func(t *testing.T, result *provisioning.DeployPreviewResult) {
				require.NotNil(t, result)
				assert.Nil(t, result.Preview)
			},
		},
		{
			name: "PopulatedPreview",
			result: &azdext.ProvisioningPreviewResult{
				Preview: &azdext.ProvisioningDeploymentPreview{
					Summary: "3 resources to create, 1 to update",
				},
			},
			verify: func(t *testing.T, result *provisioning.DeployPreviewResult) {
				require.NotNil(t, result)
				require.NotNil(t, result.Preview)
				assert.Equal(t, "3 resources to create, 1 to update", result.Preview.Status)
				require.NotNil(t, result.Preview.Properties)
				assert.Empty(t, result.Preview.Properties.Changes)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertFromProtoPreviewResult(tt.result)
			tt.verify(t, result)
		})
	}
}

func Test_convertFromProtoParameters(t *testing.T) {
	tests := []struct {
		name   string
		params []*azdext.ProvisioningParameter
		verify func(t *testing.T, result []provisioning.Parameter)
	}{
		{
			name:   "Nil",
			params: nil,
			verify: func(t *testing.T, result []provisioning.Parameter) {
				assert.Empty(t, result)
			},
		},
		{
			name:   "EmptySlice",
			params: []*azdext.ProvisioningParameter{},
			verify: func(t *testing.T, result []provisioning.Parameter) {
				assert.Empty(t, result)
			},
		},
		{
			name: "MultipleParameters",
			params: []*azdext.ProvisioningParameter{
				{
					Name:  "location",
					Value: "eastus2",
				},
				{
					Name:   "adminPassword",
					Secret: true,
				},
				{
					Name:               "appName",
					Value:              "my-app",
					EnvVarMapping:      []string{"AZURE_APP_NAME", "APP_NAME"},
					LocalPrompt:        true,
					UsingEnvVarMapping: true,
				},
			},
			verify: func(t *testing.T, result []provisioning.Parameter) {
				require.Len(t, result, 3)

				assert.Equal(t, "location", result[0].Name)
				assert.Equal(t, "eastus2", result[0].Value)
				assert.False(t, result[0].Secret)

				assert.Equal(t, "adminPassword", result[1].Name)
				assert.True(t, result[1].Secret)

				assert.Equal(t, "appName", result[2].Name)
				assert.Equal(t, "my-app", result[2].Value)
				assert.Equal(t, []string{"AZURE_APP_NAME", "APP_NAME"}, result[2].EnvVarMapping)
				assert.True(t, result[2].LocalPrompt)
				assert.True(t, result[2].UsingEnvVarMapping)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertFromProtoParameters(tt.params)
			tt.verify(t, result)
		})
	}
}

func Test_convertFromProtoPreviewResultWithChanges(t *testing.T) {
	result := convertFromProtoPreviewResult(&azdext.ProvisioningPreviewResult{
		Preview: &azdext.ProvisioningDeploymentPreview{
			Summary: "2 changes",
			Changes: []*azdext.ProvisioningDeploymentPreviewChange{
				{
					ChangeType:   "Create",
					ResourceId:   "/subscriptions/s/resourceGroups/rg",
					ResourceType: "Microsoft.Resources/resourceGroups",
					Name:         "rg",
				},
				nil, // nil entries are skipped
				{
					ChangeType:   "Delete",
					ResourceType: "Microsoft.ContainerRegistry/registries",
					Name:         "cr",
				},
			},
		},
	})

	require.NotNil(t, result.Preview)
	assert.Equal(t, "2 changes", result.Preview.Status)
	require.Len(t, result.Preview.Properties.Changes, 2, "nil change entries must be skipped")

	first := result.Preview.Properties.Changes[0]
	assert.Equal(t, provisioning.ChangeTypeCreate, first.ChangeType)
	assert.Equal(t, "Microsoft.Resources/resourceGroups", first.ResourceType)
	assert.Equal(t, "rg", first.Name)
	assert.Equal(t, "/subscriptions/s/resourceGroups/rg", first.ResourceId.Id)

	assert.Equal(t, provisioning.ChangeTypeDelete, result.Preview.Properties.Changes[1].ChangeType)
}

func TestPromptService_PromptSubscription_Success(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptSubscriptionFn: func(ctx context.Context, opts *prompt.SelectOptions) (*account.Subscription, error) {
			return &account.Subscription{
				Id:                 "sub-123",
				Name:               "My Sub",
				TenantId:           "tenant-1",
				UserAccessTenantId: "user-tenant-1",
				IsDefault:          true,
			}, nil
		},
	}
	svc := newTestPromptService(mock, false)
	resp, err := svc.PromptSubscription(t.Context(), &azdext.PromptSubscriptionRequest{
		Message: "select subscription",
	})
	require.NoError(t, err)
	require.Equal(t, "sub-123", resp.Subscription.Id)
	require.Equal(t, "My Sub", resp.Subscription.Name)
	require.True(t, resp.Subscription.IsDefault)
}

func TestPromptService_PromptLocation_Success(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptLocationFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts *prompt.SelectOptions,
		) (*account.Location, error) {
			return &account.Location{
				Name:                "westus2",
				DisplayName:         "West US 2",
				RegionalDisplayName: "(US) West US 2",
			}, nil
		},
	}
	svc := newTestPromptService(mock, false)
	resp, err := svc.PromptLocation(t.Context(), &azdext.PromptLocationRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "westus2", resp.Location.Name)
	require.Equal(t, "West US 2", resp.Location.DisplayName)
}

func TestPromptService_PromptLocation_WithAllowedLocations(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptLocationFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts *prompt.SelectOptions,
		) (*account.Location, error) {
			return &account.Location{Name: "eastus"}, nil
		},
	}
	svc := newTestPromptService(mock, false)
	resp, err := svc.PromptLocation(t.Context(), &azdext.PromptLocationRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
		AllowedLocations: []string{"eastus", "westus"},
	})
	require.NoError(t, err)
	require.Equal(t, "eastus", resp.Location.Name)
}

func TestPromptService_PromptResourceGroup_Success(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptResourceGroupFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts *prompt.ResourceGroupOptions,
		) (*azapi.ResourceGroup, error) {
			return &azapi.ResourceGroup{
				Id:       "/subscriptions/sub/resourceGroups/rg-1",
				Name:     "rg-1",
				Location: "westus2",
			}, nil
		},
	}
	svc := newTestPromptService(mock, false)
	resp, err := svc.PromptResourceGroup(t.Context(), &azdext.PromptResourceGroupRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "rg-1", resp.ResourceGroup.Name)
}

func TestPromptService_PromptSubscriptionResource_Success(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptSubscriptionResourceFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
		) (*azapi.ResourceExtended, error) {
			return &azapi.ResourceExtended{
				Resource: azapi.Resource{
					Id: "/sub/res-1", Name: "res-1",
					Type: "Microsoft.Web/sites", Location: "eastus",
				},
				Kind: "app",
			}, nil
		},
	}
	svc := newTestPromptService(mock, false)
	resp, err := svc.PromptSubscriptionResource(t.Context(), &azdext.PromptSubscriptionResourceRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
		Options: &azdext.PromptResourceOptions{
			ResourceType: "Microsoft.Web/sites",
			SelectOptions: &azdext.PromptResourceSelectOptions{
				AllowNewResource: new(false),
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "res-1", resp.Resource.Name)
	require.Equal(t, "app", resp.Resource.Kind)
}

func TestPromptService_PromptResourceGroupResource_Success(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptResourceGroupResourceFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
		) (*azapi.ResourceExtended, error) {
			return &azapi.ResourceExtended{
				Resource: azapi.Resource{
					Id: "/sub/rg/res-2", Name: "res-2",
					Type: "Microsoft.Storage/storageAccounts", Location: "westus",
				},
				Kind: "StorageV2",
			}, nil
		},
	}
	svc := newTestPromptService(mock, false)
	resp, err := svc.PromptResourceGroupResource(t.Context(), &azdext.PromptResourceGroupResourceRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "res-2", resp.Resource.Name)
}
