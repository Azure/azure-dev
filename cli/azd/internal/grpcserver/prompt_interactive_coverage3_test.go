// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// mockPromptService implements prompt.PromptService for testing.
type mockPromptService struct {
	promptSubscriptionFn func(ctx context.Context, opts *prompt.SelectOptions) (*account.Subscription, error)
	promptLocationFn     func(
		ctx context.Context, ac *prompt.AzureContext, opts *prompt.SelectOptions,
	) (*account.Location, error)
	promptResourceGroupFn func(
		ctx context.Context, ac *prompt.AzureContext, opts *prompt.ResourceGroupOptions,
	) (*azapi.ResourceGroup, error)
	promptSubscriptionResourceFn func(
		ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
	) (*azapi.ResourceExtended, error)
	promptResourceGroupResourceFn func(
		ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
	) (*azapi.ResourceExtended, error)
}

func (m *mockPromptService) PromptSubscription(
	ctx context.Context, opts *prompt.SelectOptions,
) (*account.Subscription, error) {
	if m.promptSubscriptionFn != nil {
		return m.promptSubscriptionFn(ctx, opts)
	}
	return nil, errors.New("not implemented")
}

func (m *mockPromptService) PromptLocation(
	ctx context.Context, ac *prompt.AzureContext, opts *prompt.SelectOptions,
) (*account.Location, error) {
	if m.promptLocationFn != nil {
		return m.promptLocationFn(ctx, ac, opts)
	}
	return nil, errors.New("not implemented")
}

func (m *mockPromptService) PromptResourceGroup(
	ctx context.Context, ac *prompt.AzureContext, opts *prompt.ResourceGroupOptions,
) (*azapi.ResourceGroup, error) {
	if m.promptResourceGroupFn != nil {
		return m.promptResourceGroupFn(ctx, ac, opts)
	}
	return nil, errors.New("not implemented")
}

func (m *mockPromptService) PromptSubscriptionResource(
	ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
) (*azapi.ResourceExtended, error) {
	if m.promptSubscriptionResourceFn != nil {
		return m.promptSubscriptionResourceFn(ctx, ac, opts)
	}
	return nil, errors.New("not implemented")
}

func (m *mockPromptService) PromptResourceGroupResource(
	ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
) (*azapi.ResourceExtended, error) {
	if m.promptResourceGroupResourceFn != nil {
		return m.promptResourceGroupResourceFn(ctx, ac, opts)
	}
	return nil, errors.New("not implemented")
}

func newTestPromptService(prompter *mockPromptService, noPrompt bool) azdext.PromptServiceServer {
	return NewPromptService(prompter, nil, nil, &internal.GlobalCommandOptions{NoPrompt: noPrompt})
}

// --- Confirm tests ---

func TestPromptService_Confirm_NilRequest(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.Confirm(t.Context(), nil)
	require.Error(t, err)
}

func TestPromptService_Confirm_NilOptions(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.Confirm(t.Context(), &azdext.ConfirmRequest{})
	require.Error(t, err)
}

func TestPromptService_Confirm_NoPrompt_WithDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.Confirm(t.Context(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "continue?",
			DefaultValue: new(true),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Value)
	require.True(t, *resp.Value)
}

func TestPromptService_Confirm_NoPrompt_NoDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.Confirm(t.Context(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message: "continue?",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no default response")
}

// --- Select tests ---

func TestPromptService_Select_NilRequest(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.Select(t.Context(), nil)
	require.Error(t, err)
}

func TestPromptService_Select_NilOptions(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.Select(t.Context(), &azdext.SelectRequest{})
	require.Error(t, err)
}

func TestPromptService_Select_NoPrompt_WithDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.Select(t.Context(), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "choose:",
			SelectedIndex: proto.Int32(2),
			Choices: []*azdext.SelectChoice{
				{Value: "a", Label: "A"},
				{Value: "b", Label: "B"},
				{Value: "c", Label: "C"},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Value)
	require.Equal(t, int32(2), *resp.Value)
}

func TestPromptService_Select_NoPrompt_NoDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.Select(t.Context(), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "choose:",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no default selection")
}

// --- MultiSelect tests ---

func TestPromptService_MultiSelect_NilRequest(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.MultiSelect(t.Context(), nil)
	require.Error(t, err)
}

func TestPromptService_MultiSelect_NilOptions(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.MultiSelect(t.Context(), &azdext.MultiSelectRequest{})
	require.Error(t, err)
}

func TestPromptService_MultiSelect_NoPrompt_ReturnsSelected(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.MultiSelect(t.Context(), &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: "pick:",
			Choices: []*azdext.MultiSelectChoice{
				{Value: "a", Label: "A", Selected: true},
				{Value: "b", Label: "B", Selected: false},
				{Value: "c", Label: "C", Selected: true},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.Values, 2)
	require.Equal(t, "a", resp.Values[0].Value)
	require.Equal(t, "c", resp.Values[1].Value)
}

func TestPromptService_MultiSelect_NoPrompt_NoneSelected(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.MultiSelect(t.Context(), &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: "pick:",
			Choices: []*azdext.MultiSelectChoice{
				{Value: "a", Label: "A", Selected: false},
			},
		},
	})
	require.NoError(t, err)
	require.Empty(t, resp.Values)
}

// --- Prompt tests ---

func TestPromptService_Prompt_NoPrompt_WithDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.Prompt(t.Context(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "enter value:",
			DefaultValue: "mydefault",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "mydefault", resp.Value)
}

func TestPromptService_Prompt_NoPrompt_RequiredNoDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.Prompt(t.Context(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:  "enter:",
			Required: true,
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no default response")
}

func TestPromptService_Prompt_NoPrompt_RequiredWithDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.Prompt(t.Context(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "enter:",
			Required:     true,
			DefaultValue: "provided",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "provided", resp.Value)
}

// --- PromptSubscription tests ---

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

func TestPromptService_PromptSubscription_Error(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptSubscriptionFn: func(ctx context.Context, opts *prompt.SelectOptions) (*account.Subscription, error) {
			return nil, errors.New("no subscriptions")
		},
	}
	svc := newTestPromptService(mock, false)
	_, err := svc.PromptSubscription(t.Context(), &azdext.PromptSubscriptionRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no subscriptions")
}

// --- PromptLocation tests ---

func TestPromptService_PromptLocation_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.PromptLocation(t.Context(), &azdext.PromptLocationRequest{})
	require.Error(t, err)
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

func TestPromptService_PromptLocation_Error(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptLocationFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts *prompt.SelectOptions,
		) (*account.Location, error) {
			return nil, errors.New("location error")
		},
	}
	svc := newTestPromptService(mock, false)
	_, err := svc.PromptLocation(t.Context(), &azdext.PromptLocationRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.Error(t, err)
}

// --- PromptResourceGroup tests ---

func TestPromptService_PromptResourceGroup_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.PromptResourceGroup(t.Context(), &azdext.PromptResourceGroupRequest{})
	require.Error(t, err)
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

func TestPromptService_PromptResourceGroup_Error(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptResourceGroupFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts *prompt.ResourceGroupOptions,
		) (*azapi.ResourceGroup, error) {
			return nil, errors.New("rg error")
		},
	}
	svc := newTestPromptService(mock, false)
	_, err := svc.PromptResourceGroup(t.Context(), &azdext.PromptResourceGroupRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.Error(t, err)
}

// --- PromptSubscriptionResource tests ---

func TestPromptService_PromptSubscriptionResource_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.PromptSubscriptionResource(t.Context(), &azdext.PromptSubscriptionResourceRequest{})
	require.Error(t, err)
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

func TestPromptService_PromptSubscriptionResource_Error(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptSubscriptionResourceFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
		) (*azapi.ResourceExtended, error) {
			return nil, errors.New("resource error")
		},
	}
	svc := newTestPromptService(mock, false)
	_, err := svc.PromptSubscriptionResource(t.Context(), &azdext.PromptSubscriptionResourceRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.Error(t, err)
}

// --- PromptResourceGroupResource tests ---

func TestPromptService_PromptResourceGroupResource_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.PromptResourceGroupResource(t.Context(), &azdext.PromptResourceGroupResourceRequest{})
	require.Error(t, err)
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

func TestPromptService_PromptResourceGroupResource_Error(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptResourceGroupResourceFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
		) (*azapi.ResourceExtended, error) {
			return nil, errors.New("rg resource error")
		},
	}
	svc := newTestPromptService(mock, false)
	_, err := svc.PromptResourceGroupResource(t.Context(), &azdext.PromptResourceGroupResourceRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.Error(t, err)
}
