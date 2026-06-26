// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
)

func Test_formatSubscriptionOptions(t *testing.T) {
	t.Run("no default config set", func(t *testing.T) {
		subscriptions := []account.Subscription{
			{
				Id:                 "1",
				Name:               "sub1",
				TenantId:           "",
				UserAccessTenantId: "",
				IsDefault:          false,
			},
		}

		subList, subs, result := formatSubscriptionOptions(subscriptions, "")

		require.EqualValues(t, 1, len(subList))
		require.EqualValues(t, 1, len(subs))
		require.EqualValues(t, nil, result)
	})

	t.Run("default value set", func(t *testing.T) {
		defaultSubId := "SUBSCRIPTION_DEFAULT"
		subscriptions := []account.Subscription{
			{
				Id:                 defaultSubId,
				Name:               "DISPLAY DEFAULT",
				TenantId:           "TENANT",
				UserAccessTenantId: "USER_TENANT",
				IsDefault:          true,
			},
			{
				Id:                 "SUBSCRIPTION_OTHER",
				Name:               "DISPLAY OTHER",
				TenantId:           "TENANT",
				UserAccessTenantId: "USER_TENANT",
				IsDefault:          false,
			},
		}

		subList, subs, result := formatSubscriptionOptions(subscriptions, defaultSubId)

		require.EqualValues(t, 2, len(subList))
		require.EqualValues(t, 2, len(subs))
		require.NotNil(t, result)
		defSub, ok := result.(string)
		require.True(t, ok)
		require.EqualValues(t, " 1. DISPLAY DEFAULT (SUBSCRIPTION_DEFAULT)", defSub)
	})
}

func newTestPrompter(t *testing.T, mockAccount *mockaccount.MockAccountManager) (*DefaultPrompter, *mocks.MockContext) {
	t.Helper()
	mockContext := mocks.NewMockContext(t.Context())
	env := environment.New("test")
	resourceService := azapi.NewResourceService(
		mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)

	p := NewDefaultPrompter(
		env, mockContext.Console, mockAccount, nil, resourceService, cloud.AzurePublic(),
	).(*DefaultPrompter)

	return p, mockContext
}

func TestDefaultPrompter_PromptSubscription_NoSubscriptions(t *testing.T) {
	t.Parallel()

	mockAccount := &mockaccount.MockAccountManager{Subscriptions: []account.Subscription{}}
	p, _ := newTestPrompter(t, mockAccount)

	subId, err := p.PromptSubscription(t.Context(), "Select a subscription")
	require.Error(t, err)
	require.Empty(t, subId)
	require.Contains(t, err.Error(), "no subscriptions found")
}

func TestDefaultPrompter_PromptSubscription_HappyPath(t *testing.T) {
	t.Parallel()

	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-alpha", Name: "Alpha", TenantId: "tenant-1", UserAccessTenantId: "tenant-1"},
			{Id: "sub-bravo", Name: "Bravo", TenantId: "tenant-1", UserAccessTenantId: "tenant-1"},
		},
	}
	p, mockCtx := newTestPrompter(t, mockAccount)

	mockCtx.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "Select a subscription")
	}).Respond(1) // pick second (Bravo, already sorted case-insensitively)

	subId, err := p.PromptSubscription(t.Context(), "Select a subscription")
	require.NoError(t, err)
	require.Equal(t, "sub-bravo", subId)
	// Because no default was set, the prompter should set the selection as default.
	require.True(t, mockAccount.HasDefaultSubscription())
	require.Equal(t, "sub-bravo", mockAccount.DefaultSubscription)
}

func TestDefaultPrompter_PromptSubscription_SelectError(t *testing.T) {
	t.Parallel()

	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-1", Name: "One"},
		},
	}
	p, mockCtx := newTestPrompter(t, mockAccount)

	mockCtx.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(input.ConsoleOptions) (any, error) { return 0, errors.New("boom") })

	subId, err := p.PromptSubscription(t.Context(), "Select a subscription")
	require.Error(t, err)
	require.Empty(t, subId)
	require.Contains(t, err.Error(), "reading subscription id")
}

func TestDefaultPrompter_PromptSubscription_AlreadyHasDefault(t *testing.T) {
	t.Parallel()

	// If the account already has a default subscription set, the prompter must not overwrite it.
	mockAccount := &mockaccount.MockAccountManager{
		DefaultSubscription: "sub-existing",
		Subscriptions: []account.Subscription{
			{Id: "sub-existing", Name: "Existing"},
			{Id: "sub-other", Name: "Other"},
		},
	}
	p, mockCtx := newTestPrompter(t, mockAccount)

	mockCtx.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return true
	}).Respond(1)

	subId, err := p.PromptSubscription(t.Context(), "Select a subscription")
	require.NoError(t, err)
	require.Equal(t, "sub-other", subId)
	require.Equal(t, "sub-existing", mockAccount.DefaultSubscription)
}

func TestDefaultPrompter_PromptLocation_HappyPath(t *testing.T) {
	t.Parallel()

	mockAccount := &mockaccount.MockAccountManager{
		Locations: []account.Location{
			{Name: "eastus", DisplayName: "East US", RegionalDisplayName: "(US) East US"},
			{Name: "westus", DisplayName: "West US", RegionalDisplayName: "(US) West US"},
		},
	}
	p, mockCtx := newTestPrompter(t, mockAccount)

	mockCtx.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "location")
	}).Respond(0) // first alphabetical by RegionalDisplayName -> eastus

	loc, err := p.PromptLocation(t.Context(), "sub-1", "Select a location", nil, nil)
	require.NoError(t, err)
	require.Equal(t, "eastus", loc)
	require.True(t, mockAccount.HasDefaultLocation())
	require.Equal(t, "eastus", mockAccount.DefaultLocation)
}

func TestDefaultPrompter_PromptLocation_FilterExcludes(t *testing.T) {
	t.Parallel()

	mockAccount := &mockaccount.MockAccountManager{
		Locations: []account.Location{
			{Name: "eastus", DisplayName: "East US", RegionalDisplayName: "(US) East US"},
			{Name: "westus", DisplayName: "West US", RegionalDisplayName: "(US) West US"},
		},
	}
	p, mockCtx := newTestPrompter(t, mockAccount)

	// Only west US should be shown (filter excludes eastus).
	filter := func(loc account.Location) bool { return loc.Name != "eastus" }

	var shownOptions []string
	mockCtx.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		shownOptions = opts.Options
		return true
	}).Respond(0)

	loc, err := p.PromptLocation(t.Context(), "sub-1", "Select a location", filter, nil)
	require.NoError(t, err)
	require.Equal(t, "westus", loc)
	require.Len(t, shownOptions, 1)
	require.Contains(t, shownOptions[0], "West US")
}

func TestDefaultPrompter_PromptLocation_WithDefaultAlreadySet(t *testing.T) {
	t.Parallel()

	// Pre-existing default should be left alone.
	mockAccount := &mockaccount.MockAccountManager{
		DefaultLocation: "westus",
		Locations: []account.Location{
			{Name: "eastus", DisplayName: "East US", RegionalDisplayName: "(US) East US"},
			{Name: "westus", DisplayName: "West US", RegionalDisplayName: "(US) West US"},
		},
	}
	p, mockCtx := newTestPrompter(t, mockAccount)

	mockCtx.Console.WhenSelect(func(opts input.ConsoleOptions) bool { return true }).Respond(0)

	loc, err := p.PromptLocation(t.Context(), "sub-1", "Select a location", nil, nil)
	require.NoError(t, err)
	require.Equal(t, "eastus", loc)
	require.Equal(t, "westus", mockAccount.DefaultLocation) // not overwritten
}

func TestDefaultPrompter_PromptLocation_SelectError(t *testing.T) {
	t.Parallel()

	mockAccount := &mockaccount.MockAccountManager{
		Locations: []account.Location{
			{Name: "eastus", DisplayName: "East US", RegionalDisplayName: "(US) East US"},
		},
	}
	p, mockCtx := newTestPrompter(t, mockAccount)

	mockCtx.Console.WhenSelect(func(opts input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return 0, errors.New("cancelled") })

	loc, err := p.PromptLocation(t.Context(), "sub-1", "Select a location", nil, nil)
	require.Error(t, err)
	require.Empty(t, loc)
}

func TestDefaultPrompter_PromptLocation_WithDefaultSelectedLocation(t *testing.T) {
	t.Parallel()

	mockAccount := &mockaccount.MockAccountManager{
		Locations: []account.Location{
			{Name: "eastus", DisplayName: "East US", RegionalDisplayName: "(US) East US"},
			{Name: "westus", DisplayName: "West US", RegionalDisplayName: "(US) West US"},
		},
	}
	p, mockCtx := newTestPrompter(t, mockAccount)

	defaultLoc := "westus"

	var defaultValue any
	mockCtx.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		defaultValue = opts.DefaultValue
		return true
	}).Respond(1)

	loc, err := p.PromptLocation(t.Context(), "sub-1", "Select a location", nil, &defaultLoc)
	require.NoError(t, err)
	require.Equal(t, "westus", loc)
	require.NotNil(t, defaultValue)
	require.Contains(t, defaultValue.(string), "West US")
}

func newTestPrompterWithConfig(
	t *testing.T,
	mockAccount *mockaccount.MockAccountManager,
	ucm config.UserConfigManager,
) (*DefaultPrompter, *mocks.MockContext) {
	t.Helper()
	mockContext := mocks.NewMockContext(t.Context())
	env := environment.New("test")
	resourceService := azapi.NewResourceService(
		mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)

	p := NewDefaultPrompter(
		env, mockContext.Console, mockAccount, ucm, resourceService, cloud.AzurePublic(),
	).(*DefaultPrompter)

	return p, mockContext
}

func TestDefaultPrompter_PromptSubscription_FilterApplied(t *testing.T) {
	t.Parallel()

	// Two subs in the same tenant; filter includes only sub-alpha.
	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-alpha", Name: "Alpha", TenantId: "t1", UserAccessTenantId: "t1"},
			{Id: "sub-bravo", Name: "Bravo", TenantId: "t1", UserAccessTenantId: "t1"},
		},
	}

	cfg := config.NewEmptyConfig()
	_ = SaveSubscriptionFilter(cfg, "t1", []string{"sub-alpha"})
	ucm := newInMemoryUserConfigManager(cfg)

	p, mockCtx := newTestPrompterWithConfig(t, mockAccount, ucm)

	// Capture the options presented and pick the first (Alpha).
	var shownOptions []string
	mockCtx.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		shownOptions = opts.Options
		return strings.Contains(opts.Message, "sub")
	}).Respond(0)

	subId, err := p.PromptSubscription(t.Context(), "Select a sub")
	require.NoError(t, err)
	require.Equal(t, "sub-alpha", subId)
	// Should show only 1 sub + "Show all subscriptions"
	require.Len(t, shownOptions, 2)
	require.Equal(t, " 2. "+ShowAllSubscriptionsOption, shownOptions[len(shownOptions)-1])
}

func TestDefaultPrompter_PromptSubscription_ShowAll(t *testing.T) {
	t.Parallel()

	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-alpha", Name: "Alpha", TenantId: "t1", UserAccessTenantId: "t1"},
			{Id: "sub-bravo", Name: "Bravo", TenantId: "t1", UserAccessTenantId: "t1"},
		},
	}

	cfg := config.NewEmptyConfig()
	_ = SaveSubscriptionFilter(cfg, "t1", []string{"sub-alpha"})
	ucm := newInMemoryUserConfigManager(cfg)

	p, mockCtx := newTestPrompterWithConfig(t, mockAccount, ucm)

	selectCount := 0
	mockCtx.Console.WhenSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "sub")
	}).RespondFn(func(opts input.ConsoleOptions) (any, error) {
		selectCount++
		if selectCount == 1 {
			// Pick "Show all subscriptions" (last option)
			return len(opts.Options) - 1, nil
		}
		// Second prompt shows all subs; pick Bravo (index 1 after sort)
		return 1, nil
	})

	subId, err := p.PromptSubscription(t.Context(), "Select a sub")
	require.NoError(t, err)
	require.Equal(t, "sub-bravo", subId)
	require.Equal(t, 2, selectCount)
}

func TestDefaultPrompter_FormatSubscriptionOptions_DemoMode(t *testing.T) {
	t.Setenv("AZD_DEMO_MODE", "true")

	subscriptions := []account.Subscription{
		{Id: "sub-secret", Name: "Display Only"},
	}

	opts, subs, _ := formatSubscriptionOptions(subscriptions, "")
	require.Len(t, opts, 1)
	require.Len(t, subs, 1)
	// In demo mode, id must not be exposed.
	require.NotContains(t, opts[0], "sub-secret")
	require.Contains(t, opts[0], "Display Only")
}

func TestDefaultPrompter_FormatSubscriptionOptions_EnvVarDefault(t *testing.T) {
	subscriptions := []account.Subscription{
		{Id: "sub-env", Name: "From Env"},
		{Id: "sub-config", Name: "From Config"},
	}

	// env var default takes precedence
	_, _, def := formatSubscriptionOptions(subscriptions, "sub-env")
	require.NotNil(t, def)
	require.Contains(t, def.(string), "From Env")
}
