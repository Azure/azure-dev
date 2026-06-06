// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/stretchr/testify/require"
)

func TestExtractUniqueTenants_Empty(t *testing.T) {
	tenants := extractUniqueTenants(nil, nil)
	require.Empty(t, tenants)
}

func TestExtractUniqueTenants_SingleTenant(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", UserAccessTenantId: "tid-1"},
		{Id: "sub-2", UserAccessTenantId: "tid-1"},
	}

	tenants := extractUniqueTenants(subs, map[string]string{"tid-1": "Contoso"})
	require.Len(t, tenants, 1)
	require.Equal(t, "tid-1", tenants[0].Id)
	require.Equal(t, "Contoso", tenants[0].DisplayName)
	require.Equal(t, 2, tenants[0].SubscriptionCount)
}

func TestExtractUniqueTenants_MultipleTenants(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", UserAccessTenantId: "tid-1"},
		{Id: "sub-2", UserAccessTenantId: "tid-2"},
		{Id: "sub-3", UserAccessTenantId: "tid-1"},
	}

	names := map[string]string{
		"tid-1": "Contoso",
		"tid-2": "Fabrikam",
	}

	tenants := extractUniqueTenants(subs, names)
	require.Len(t, tenants, 2)
	// Sorted alphabetically by display name
	require.Equal(t, "Contoso", tenants[0].DisplayName)
	require.Equal(t, 2, tenants[0].SubscriptionCount)
	require.Equal(t, "Fabrikam", tenants[1].DisplayName)
	require.Equal(t, 1, tenants[1].SubscriptionCount)
}

func TestExtractUniqueTenants_FallbackToTenantId(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", TenantId: "tid-1", UserAccessTenantId: ""},
	}

	tenants := extractUniqueTenants(subs, nil)
	require.Len(t, tenants, 1)
	require.Equal(t, "tid-1", tenants[0].Id)
	// Display name falls back to the ID when no names provided
	require.Equal(t, "tid-1", tenants[0].DisplayName)
}

func TestExtractUniqueTenants_NoDisplayNames(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", UserAccessTenantId: "tid-1"},
		{Id: "sub-2", UserAccessTenantId: "tid-2"},
	}

	tenants := extractUniqueTenants(subs, nil)
	require.Len(t, tenants, 2)
	require.Equal(t, "tid-1", tenants[0].DisplayName)
	require.Equal(t, "tid-2", tenants[1].DisplayName)
}

func TestFilterSubscriptionsByTenant_EmptyTenantId(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", UserAccessTenantId: "tid-1"},
		{Id: "sub-2", UserAccessTenantId: "tid-2"},
	}

	result := filterSubscriptionsByTenant(subs, "")
	require.Len(t, result, 2)
}

func TestFilterSubscriptionsByTenant_Filtered(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", UserAccessTenantId: "tid-1"},
		{Id: "sub-2", UserAccessTenantId: "tid-2"},
		{Id: "sub-3", UserAccessTenantId: "tid-1"},
	}

	result := filterSubscriptionsByTenant(subs, "tid-1")
	require.Len(t, result, 2)
	require.Equal(t, "sub-1", result[0].Id)
	require.Equal(t, "sub-3", result[1].Id)
}

func TestFilterSubscriptionsByTenant_NoMatch(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", UserAccessTenantId: "tid-1"},
	}

	result := filterSubscriptionsByTenant(subs, "tid-unknown")
	require.Empty(t, result)
}

func TestFilterByTenantEnvVar_NotSet(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", UserAccessTenantId: "tid-1"},
		{Id: "sub-2", UserAccessTenantId: "tid-2"},
	}

	result := filterByTenantEnvVar(subs)
	require.Len(t, result, 2)
}

func TestFilterByTenantEnvVar_Set(t *testing.T) {
	t.Setenv("AZURE_TENANT_ID", "tid-1")

	subs := []account.Subscription{
		{Id: "sub-1", UserAccessTenantId: "tid-1"},
		{Id: "sub-2", UserAccessTenantId: "tid-2"},
	}

	result := filterByTenantEnvVar(subs)
	require.Len(t, result, 1)
	require.Equal(t, "sub-1", result[0].Id)
}

func TestFilterByTenantEnvVar_NoMatchFallsBack(t *testing.T) {
	t.Setenv("AZURE_TENANT_ID", "tid-unknown")

	subs := []account.Subscription{
		{Id: "sub-1", UserAccessTenantId: "tid-1"},
	}

	// Falls back to showing all when the env var doesn't match
	result := filterByTenantEnvVar(subs)
	require.Len(t, result, 1)
}

func TestPromptTenantSelection_SingleTenant(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	tenants := []tenantInfo{
		{Id: "tid-1", DisplayName: "Contoso", SubscriptionCount: 3},
	}

	selected, err := promptTenantSelection(t.Context(), mockContext.Console, tenants)
	require.NoError(t, err)
	require.Equal(t, "tid-1", selected)
}

func TestPromptTenantSelection_MultipleTenants_SelectFirst(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	mockContext.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "Select a tenant")
	}).Respond(0) // pick first tenant

	tenants := []tenantInfo{
		{Id: "tid-1", DisplayName: "Contoso", SubscriptionCount: 3},
		{Id: "tid-2", DisplayName: "Fabrikam", SubscriptionCount: 1},
	}

	selected, err := promptTenantSelection(t.Context(), mockContext.Console, tenants)
	require.NoError(t, err)
	require.Equal(t, "tid-1", selected)
}

func TestPromptTenantSelection_MultipleTenants_SelectAllTenants(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	mockContext.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "Select a tenant")
	}).Respond(2) // pick "All tenants" (third option with 2 tenants)

	tenants := []tenantInfo{
		{Id: "tid-1", DisplayName: "Contoso", SubscriptionCount: 3},
		{Id: "tid-2", DisplayName: "Fabrikam", SubscriptionCount: 1},
	}

	selected, err := promptTenantSelection(t.Context(), mockContext.Console, tenants)
	require.NoError(t, err)
	require.Empty(t, selected) // empty string = all tenants
}

func TestPromptTenantSelection_NoTenants(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	selected, err := promptTenantSelection(t.Context(), mockContext.Console, nil)
	require.NoError(t, err)
	require.Empty(t, selected)
}

func TestPromptSubscription_MultiTenant_TenantPickerShown(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-1", Name: "Alpha", UserAccessTenantId: "tid-1"},
			{Id: "sub-2", Name: "Bravo", UserAccessTenantId: "tid-2"},
			{Id: "sub-3", Name: "Charlie", UserAccessTenantId: "tid-1"},
		},
	}

	p, _ := newTestPrompterWithCtx(t, mockAccount, mockContext)

	// First prompt: select tenant (pick tid-1)
	mockContext.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "Select a tenant")
	}).Respond(0) // first tenant

	// Second prompt: select subscription from filtered list
	mockContext.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "Select a subscription")
	}).Respond(1) // pick second in filtered list (Charlie after Alpha alphabetically)

	subId, err := p.PromptSubscription(t.Context(), "Select a subscription")
	require.NoError(t, err)
	// After filtering to tid-1: Alpha (sub-1) and Charlie (sub-3), sorted
	require.Equal(t, "sub-3", subId) // Charlie is second alphabetically
}

func TestPromptSubscription_MultiTenant_AllTenantsOption(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-1", Name: "Alpha", UserAccessTenantId: "tid-1"},
			{Id: "sub-2", Name: "Bravo", UserAccessTenantId: "tid-2"},
		},
	}

	p, _ := newTestPrompterWithCtx(t, mockAccount, mockContext)

	// First prompt: "All tenants" (last option, index 2 with 2 tenants)
	mockContext.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "Select a tenant")
	}).Respond(2)

	// Second prompt: select subscription from full list
	mockContext.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "Select a subscription")
	}).Respond(0) // pick first subscription

	subId, err := p.PromptSubscription(t.Context(), "Select a subscription")
	require.NoError(t, err)
	require.Equal(t, "sub-1", subId) // Alpha is first alphabetically
}

func TestPromptSubscription_NoPromptMode_SkipsTenantPicker(t *testing.T) {
	t.Setenv("AZURE_TENANT_ID", "tid-1")

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.SetNoPromptMode(true)

	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-1", Name: "Alpha", UserAccessTenantId: "tid-1"},
			{Id: "sub-2", Name: "Bravo", UserAccessTenantId: "tid-2"},
			{Id: "sub-3", Name: "Charlie", UserAccessTenantId: "tid-1"},
		},
	}

	p, _ := newTestPrompterWithCtx(t, mockAccount, mockContext)

	// In no-prompt mode the tenant picker is skipped, but AZURE_TENANT_ID
	// filtering still applies. Subscription selection still goes through
	// Console.Select (not bypassed by no-prompt in this legacy prompter path).
	mockContext.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "Select a subscription")
	}).Respond(0)

	subId, err := p.PromptSubscription(t.Context(), "Select a subscription")
	require.NoError(t, err)
	// Should be filtered to tid-1 only: Alpha and Charlie
	require.Equal(t, "sub-1", subId)
}

func newTestPrompterWithCtx(
	t *testing.T,
	mockAccount *mockaccount.MockAccountManager,
	mockCtx *mocks.MockContext,
) (*DefaultPrompter, *mocks.MockContext) {
	t.Helper()
	env := environment.New("test")
	resourceService := azapi.NewResourceService(
		mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)

	p := NewDefaultPrompter(
		env, mockCtx.Console, mockAccount, resourceService, cloud.AzurePublic(),
	).(*DefaultPrompter)

	return p, mockCtx
}
