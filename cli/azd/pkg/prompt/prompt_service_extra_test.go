// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"errors"
	"testing"

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
		auth, console, ucm, sm, rs,
	).(*promptService)

	return ps, rs, sm, console
}

// PromptResourceGroup - NoPrompt branches

func newAzCtx(scope AzureScope) *AzureContext {
	return &AzureContext{
		Scope:     scope,
		Resources: NewAzureResourceList(nil, nil),
	}
}

// PromptSubscriptionResource - NoPrompt errors

func TestPromptService_PromptSubscriptionResource_NoPrompt_Errors(t *testing.T) {
	t.Parallel()

	ps, rs, _, _ := newTestPromptService(t, true)
	rtype := azapi.AzureResourceType("Microsoft.Storage/storageAccounts")

	rs.On("ListSubscriptionResources", mock.Anything, "sub-1", mock.Anything).
		Return([]*azapi.ResourceExtended{}, nil)

	_, err := ps.PromptSubscriptionResource(t.Context(), newAzCtx(AzureScope{SubscriptionId: "sub-1"}),
		ResourceOptions{
			ResourceType:    &rtype,
			SelectorOptions: &SelectOptions{AllowNewResource: new(false)},
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no resources found with type")
}

func TestPromptService_PromptSubscriptionResource_NoPrompt_CustomDisplayName(t *testing.T) {
	t.Parallel()

	ps, rs, _, _ := newTestPromptService(t, true)

	rs.On("ListSubscriptionResources", mock.Anything, "sub-1", mock.Anything).
		Return([]*azapi.ResourceExtended{}, nil)

	_, err := ps.PromptSubscriptionResource(t.Context(), newAzCtx(AzureScope{SubscriptionId: "sub-1"}),
		ResourceOptions{
			ResourceTypeDisplayName: "My Fancy Resource",
			SelectorOptions:         &SelectOptions{AllowNewResource: new(false)},
		})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoResourcesFound)
}

func TestPromptService_PromptSubscriptionResource_NoPrompt_FallbackName(t *testing.T) {
	t.Parallel()

	ps, rs, _, _ := newTestPromptService(t, true)

	// Neither ResourceType nor ResourceTypeDisplayName provided -> fallback "resource".
	rs.On("ListSubscriptionResources", mock.Anything, "sub-1", mock.Anything).
		Return([]*azapi.ResourceExtended{}, nil)

	_, err := ps.PromptSubscriptionResource(t.Context(), newAzCtx(AzureScope{SubscriptionId: "sub-1"}),
		ResourceOptions{
			SelectorOptions: &SelectOptions{AllowNewResource: new(false)},
		})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoResourcesFound)
}

// PromptResourceGroupResource - NoPrompt errors

func TestPromptService_PromptResourceGroupResource_NoPrompt_Errors(t *testing.T) {
	t.Parallel()

	ps, rs, _, _ := newTestPromptService(t, true)
	rtype := azapi.AzureResourceType("Microsoft.Web/sites")

	rs.On("ListResourceGroupResources", mock.Anything, "sub-1", "rg-1", mock.Anything).
		Return([]*azapi.ResourceExtended{}, nil)

	_, err := ps.PromptResourceGroupResource(t.Context(),
		newAzCtx(AzureScope{SubscriptionId: "sub-1", ResourceGroup: "rg-1"}),
		ResourceOptions{
			ResourceType:    &rtype,
			SelectorOptions: &SelectOptions{AllowNewResource: new(false)},
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no resources found with type")
}

func TestPromptService_PromptResourceGroupResource_NoPrompt_CustomDisplayName(t *testing.T) {
	t.Parallel()

	ps, rs, _, _ := newTestPromptService(t, true)

	rs.On("ListResourceGroupResources", mock.Anything, "sub-1", "rg-1", mock.Anything).
		Return([]*azapi.ResourceExtended{}, nil)

	_, err := ps.PromptResourceGroupResource(t.Context(),
		newAzCtx(AzureScope{SubscriptionId: "sub-1", ResourceGroup: "rg-1"}),
		ResourceOptions{
			ResourceTypeDisplayName: "Widget",
			SelectorOptions:         &SelectOptions{AllowNewResource: new(false)},
		})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoResourcesFound)
}

func TestPromptService_PromptResourceGroupResource_NoPrompt_FallbackName(t *testing.T) {
	t.Parallel()

	ps, rs, _, _ := newTestPromptService(t, true)

	rs.On("ListResourceGroupResources", mock.Anything, "sub-1", "rg-1", mock.Anything).
		Return([]*azapi.ResourceExtended{}, nil)

	_, err := ps.PromptResourceGroupResource(t.Context(),
		newAzCtx(AzureScope{SubscriptionId: "sub-1", ResourceGroup: "rg-1"}),
		ResourceOptions{
			SelectorOptions: &SelectOptions{AllowNewResource: new(false)},
		})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoResourcesFound)
}

// PromptLocation - pre-set scope paths (already covered by existing tests but adding additional shape tests)

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

	azCtx := NewAzureContext(ps, AzureScope{}, nil, true)
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
