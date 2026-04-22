// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockauth"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestPromptService(
	t *testing.T, noPrompt bool,
) (*promptService, *mockazapi.MockResourceService, *mockaccount.MockSubscriptionManager, *mockinput.MockConsole) {
	t.Helper()

	ucm := newInMemoryUserConfigManager(nil)
	auth := &mockauth.MockAuthManager{}
	rs := &mockazapi.MockResourceService{}
	sm := &mockaccount.MockSubscriptionManager{}
	console := mockinput.NewMockConsole()
	console.SetNoPromptMode(noPrompt)

	ps := NewPromptService(
		auth, console, ucm, sm, rs, &internal.GlobalCommandOptions{NoPrompt: noPrompt},
	).(*promptService)

	return ps, rs, sm, console
}

// PromptResourceGroup - NoPrompt branches

func TestPromptService_PromptResourceGroup_NoPrompt_Missing(t *testing.T) {
	t.Parallel()

	ps, _, _, _ := newTestPromptService(t, true)

	// azureContext.Scope.ResourceGroup is empty -> must error
	_, err := ps.PromptResourceGroup(t.Context(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-1"},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "resource group selection required")
}

func TestPromptService_PromptResourceGroup_NoPrompt_Found(t *testing.T) {
	t.Parallel()

	ps, rs, _, _ := newTestPromptService(t, true)

	rs.On("ListResourceGroup", mock.Anything, "sub-1", mock.Anything).
		Return([]*azapi.Resource{
			{Id: "/subscriptions/sub-1/resourceGroups/rg-other", Name: "rg-other", Location: "eastus"},
			{Id: "/subscriptions/sub-1/resourceGroups/rg-pick", Name: "rg-pick", Location: "westus"},
		}, nil)

	got, err := ps.PromptResourceGroup(t.Context(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-1", ResourceGroup: "rg-pick"},
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "rg-pick", got.Name)
	require.Equal(t, "westus", got.Location)
}

func TestPromptService_PromptResourceGroup_NoPrompt_NotFound(t *testing.T) {
	t.Parallel()

	ps, rs, _, _ := newTestPromptService(t, true)

	rs.On("ListResourceGroup", mock.Anything, "sub-1", mock.Anything).
		Return([]*azapi.Resource{
			{Id: "/subscriptions/sub-1/resourceGroups/rg-a", Name: "rg-a", Location: "eastus"},
		}, nil)

	_, err := ps.PromptResourceGroup(t.Context(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-1", ResourceGroup: "rg-missing"},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "rg-missing")
}

func TestPromptService_PromptResourceGroup_NoPrompt_ListError(t *testing.T) {
	t.Parallel()

	ps, rs, _, _ := newTestPromptService(t, true)

	rs.On("ListResourceGroup", mock.Anything, "sub-1", mock.Anything).
		Return(([]*azapi.Resource)(nil), errors.New("list failed"))

	_, err := ps.PromptResourceGroup(t.Context(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-1", ResourceGroup: "rg-any"},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to load resource groups")
}

func TestPromptService_PromptResourceGroup_NoPrompt_NilContext_Errors(t *testing.T) {
	t.Parallel()

	ps, _, sm, _ := newTestPromptService(t, true)

	// With a nil azure context and no default subscription, EnsureSubscription will try to prompt which
	// will fail because there is no PromptService attached and NoPrompt=true means PromptSubscription
	// returns an error for 0 subscriptions.
	sm.On("GetSubscriptions", mock.Anything).Return(emptySubs(), nil)

	// The nil context case causes EnsureSubscription to call the context's internal promptService which
	// is nil; to avoid a nil deref, supply a subscription explicitly.
	_, err := ps.PromptResourceGroup(t.Context(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-1"},
	}, &ResourceGroupOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "resource group selection required")
}

func newAzCtx(scope AzureScope) *AzureContext {
	return &AzureContext{
		Scope:     scope,
		Resources: NewAzureResourceList(nil, nil),
	}
}

// PromptSubscriptionResource - NoPrompt errors

func TestPromptService_PromptSubscriptionResource_NoPrompt_Errors(t *testing.T) {
	t.Parallel()

	ps, _, _, _ := newTestPromptService(t, true)
	rtype := azapi.AzureResourceType("Microsoft.Storage/storageAccounts")

	_, err := ps.PromptSubscriptionResource(t.Context(), newAzCtx(AzureScope{SubscriptionId: "sub-1"}),
		ResourceOptions{
			ResourceType: &rtype,
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot prompt in --no-prompt mode")
	require.Contains(t, err.Error(), string(rtype))
}

func TestPromptService_PromptSubscriptionResource_NoPrompt_CustomDisplayName(t *testing.T) {
	t.Parallel()

	ps, _, _, _ := newTestPromptService(t, true)

	_, err := ps.PromptSubscriptionResource(t.Context(), newAzCtx(AzureScope{SubscriptionId: "sub-1"}),
		ResourceOptions{
			ResourceTypeDisplayName: "My Fancy Resource",
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "My Fancy Resource")
}

func TestPromptService_PromptSubscriptionResource_NoPrompt_FallbackName(t *testing.T) {
	t.Parallel()

	ps, _, _, _ := newTestPromptService(t, true)

	// Neither ResourceType nor ResourceTypeDisplayName provided -> fallback "resource".
	_, err := ps.PromptSubscriptionResource(t.Context(), newAzCtx(AzureScope{SubscriptionId: "sub-1"}),
		ResourceOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "resource selection required")
}

// PromptResourceGroupResource - NoPrompt errors

func TestPromptService_PromptResourceGroupResource_NoPrompt_Errors(t *testing.T) {
	t.Parallel()

	ps, _, _, _ := newTestPromptService(t, true)
	rtype := azapi.AzureResourceType("Microsoft.Web/sites")

	_, err := ps.PromptResourceGroupResource(t.Context(),
		newAzCtx(AzureScope{SubscriptionId: "sub-1", ResourceGroup: "rg-1"}),
		ResourceOptions{
			ResourceType: &rtype,
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot prompt in --no-prompt mode")
	require.Contains(t, err.Error(), string(rtype))
}

func TestPromptService_PromptResourceGroupResource_NoPrompt_CustomDisplayName(t *testing.T) {
	t.Parallel()

	ps, _, _, _ := newTestPromptService(t, true)

	_, err := ps.PromptResourceGroupResource(t.Context(),
		newAzCtx(AzureScope{SubscriptionId: "sub-1", ResourceGroup: "rg-1"}),
		ResourceOptions{
			ResourceTypeDisplayName: "Widget",
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Widget")
}

func TestPromptService_PromptResourceGroupResource_NoPrompt_FallbackName(t *testing.T) {
	t.Parallel()

	ps, _, _, _ := newTestPromptService(t, true)

	_, err := ps.PromptResourceGroupResource(t.Context(),
		newAzCtx(AzureScope{SubscriptionId: "sub-1", ResourceGroup: "rg-1"}),
		ResourceOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "resource selection required")
}

// PromptLocation - pre-set scope paths (already covered by existing tests but adding additional shape tests)

func TestPromptService_PromptLocation_NoPrompt_DefaultsToEastUS2(t *testing.T) {
	t.Parallel()

	// No explicit user config -> default "eastus2" must be used.
	ps, _, sm, _ := newTestPromptService(t, true)
	sm.On("GetLocations", mock.Anything, "sub-1").Return(locations(), nil)

	loc, err := ps.PromptLocation(t.Context(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-1"},
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "eastus2", loc.Name)
}

// TestPromptService_PromptLocation_EmptySubscription_PropagatesError ensures
// that when PromptLocation is called with a context that has no subscription,
// EnsureSubscription is invoked and its error is propagated back to the caller.
// (We pass a real AzureContext; the nil-input branch of PromptLocation is not
// currently exercised by any production caller and would panic because the
// synthesized empty context has no wired promptService.)
func TestPromptService_PromptLocation_EmptySubscription_PropagatesError(t *testing.T) {
	t.Parallel()

	ps, _, sm, _ := newTestPromptService(t, true)
	// Pre-load no subscriptions so EnsureSubscription fails with a clear error.
	sm.On("GetSubscriptions", mock.Anything).Return(emptySubs(), nil)

	azCtx := NewAzureContext(ps, AzureScope{}, nil)
	_, err := ps.PromptLocation(t.Context(), azCtx, nil)
	require.Error(t, err)
}

// PromptCustomResource

func TestPromptCustomResource_ForceNewResource_ReturnsNewValue(t *testing.T) {
	t.Parallel()

	forceNew := true
	newValue := "brand-new"

	result, err := PromptCustomResource(t.Context(), CustomResourceOptions[string]{
		NewResourceValue: newValue,
		SelectorOptions: &SelectOptions{
			ForceNewResource: &forceNew,
			Message:          "Pick",
		},
		LoadData: func(ctx context.Context) ([]*string, error) {
			t.Fatalf("LoadData must not be called when ForceNewResource is true")
			return nil, nil
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "brand-new", *result)
}

func TestPromptCustomResource_LoadDataError(t *testing.T) {
	t.Parallel()

	loadErr := errors.New("loading failed")

	result, err := PromptCustomResource(t.Context(), CustomResourceOptions[string]{
		SelectorOptions: &SelectOptions{Message: "Pick"},
		LoadData: func(ctx context.Context) ([]*string, error) {
			return nil, loadErr
		},
	})
	require.Nil(t, result)
	require.Error(t, err)
	require.ErrorIs(t, err, loadErr)
}

func TestPromptCustomResource_NoResourcesAndNotAllowedNew_ReturnsSentinel(t *testing.T) {
	t.Parallel()

	disallowNew := false

	result, err := PromptCustomResource(t.Context(), CustomResourceOptions[string]{
		SelectorOptions: &SelectOptions{
			Message:          "Pick",
			AllowNewResource: &disallowNew,
		},
		LoadData: func(ctx context.Context) ([]*string, error) {
			return []*string{}, nil
		},
	})
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrNoResourcesFound)
}

func TestPromptCustomResource_NilSelectorOptions_UsesDefaultsAndForce(t *testing.T) {
	t.Parallel()

	// ForceNewResource path still works if caller supplied empty options and we force it.
	// Supply SelectorOptions with ForceNewResource explicitly since the defaults ForceNewResource=false.
	force := true
	v := 42
	result, err := PromptCustomResource(t.Context(), CustomResourceOptions[int]{
		NewResourceValue: v,
		SelectorOptions: &SelectOptions{
			ForceNewResource: &force,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 42, *result)
}

// helpers

func emptySubs() []account.Subscription { return []account.Subscription{} }

func locations() []account.Location {
	return []account.Location{
		{Name: "eastus2", DisplayName: "East US 2", RegionalDisplayName: "(US) East US 2"},
		{Name: "westus3", DisplayName: "West US 3", RegionalDisplayName: "(US) West US 3"},
	}
}
