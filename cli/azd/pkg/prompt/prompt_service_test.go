// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockauth"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// inMemoryUserConfigManager is an in-memory implementation of config.UserConfigManager for tests,
// avoiding reads from the real user config file on disk.
type inMemoryUserConfigManager struct {
	cfg config.Config
}

func newInMemoryUserConfigManager(cfg config.Config) *inMemoryUserConfigManager {
	if cfg == nil {
		cfg = config.NewEmptyConfig()
	}
	return &inMemoryUserConfigManager{cfg: cfg}
}

func (m *inMemoryUserConfigManager) Load() (config.Config, error) { return m.cfg, nil }
func (m *inMemoryUserConfigManager) Save(_ config.Config) error   { return nil }

func Test_PromptService_PromptSubscription(t *testing.T) {
	ucm := newInMemoryUserConfigManager(nil)

	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}
	mockConsole := mockinput.NewMockConsole()

	tokenClaims := auth.TokenClaims{
		TenantId: "tenant-1",
	}

	authManager.
		On("ClaimsForCurrentUser", mock.Anything, mock.Anything).
		Return(tokenClaims, nil)

	subscriptionManager.
		On("GetSubscriptions", mock.Anything, tokenClaims.TenantId).
		Return([]account.Subscription{
			{
				Id:       "/subscriptions/subscription-1",
				TenantId: "tenant-1",
				Name:     "Subscription 1",
			},
			{
				Id:       "/subscriptions/subscription-2",
				TenantId: "tenant-2",
				Name:     "Subscription 2",
			},
		}, nil)

	globalOptions := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}

	promptService := NewPromptService(
		authManager, mockConsole, ucm, subscriptionManager, resourceService, globalOptions,
	)
	require.NotNil(t, promptService)
}

func Test_PromptService_PromptSubscription_NoPrompt_AutoSelect(t *testing.T) {
	tests := []struct {
		name          string
		subscriptions []account.Subscription
		defaultSubId  string
		wantErr       bool
		errContains   string
		wantSubId     string
	}{
		{
			name: "single subscription auto-selected",
			subscriptions: []account.Subscription{
				{Id: "sub-1", TenantId: "tenant-1", Name: "My Only Sub"},
			},
			wantSubId: "sub-1",
		},
		{
			name:          "no subscriptions returns error",
			subscriptions: []account.Subscription{},
			wantErr:       true,
			errContains:   "no Azure subscriptions found for the current account",
		},
		{
			name: "multiple subscriptions without default returns error",
			subscriptions: []account.Subscription{
				{Id: "sub-1", TenantId: "tenant-1", Name: "Sub 1"},
				{Id: "sub-2", TenantId: "tenant-2", Name: "Sub 2"},
			},
			wantErr:     true,
			errContains: "multiple Azure subscriptions found",
		},
		{
			name: "default subscription used when set",
			subscriptions: []account.Subscription{
				{Id: "sub-1", TenantId: "tenant-1", Name: "Sub 1"},
				{Id: "sub-2", TenantId: "tenant-2", Name: "Sub 2"},
			},
			defaultSubId: "sub-2",
			wantSubId:    "sub-2",
		},
		{
			name: "default subscription not found returns error",
			subscriptions: []account.Subscription{
				{Id: "sub-1", TenantId: "tenant-1", Name: "Sub 1"},
			},
			defaultSubId: "sub-nonexistent",
			wantErr:      true,
			errContains:  "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.NewEmptyConfig()
			if tt.defaultSubId != "" {
				err := cfg.Set("defaults.subscription", tt.defaultSubId)
				require.NoError(t, err)
			}
			ucm := newInMemoryUserConfigManager(cfg)

			authManager := &mockauth.MockAuthManager{}
			subscriptionManager := &mockaccount.MockSubscriptionManager{}
			resourceService := &mockazapi.MockResourceService{}
			mockConsole := mockinput.NewMockConsole()
			mockConsole.SetNoPromptMode(true)

			subscriptionManager.
				On("GetSubscriptions", mock.Anything).
				Return(tt.subscriptions, nil)

			globalOptions := &internal.GlobalCommandOptions{
				NoPrompt: true,
			}

			ps := NewPromptService(
				authManager, mockConsole, ucm, subscriptionManager, resourceService, globalOptions,
			)

			result, err := ps.PromptSubscription(context.Background(), nil)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, tt.wantSubId, result.Id)
			}
		})
	}
}

func TestFormatSubscriptionDisplayName_DemoModeHidesId(t *testing.T) {
	displayName := formatSubscriptionDisplayName(&account.Subscription{
		Id:   "/subscriptions/sub-1",
		Name: "Subscription 1",
	}, true)

	require.Equal(t, "Subscription 1", displayName)
}

func TestPromptSubscription_NoPrompt_AutoSelect_DemoModeRedactsOutput(t *testing.T) {
	t.Setenv("AZD_DEMO_MODE", "true")

	ucm := newInMemoryUserConfigManager(nil)
	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}
	mockConsole := mockinput.NewMockConsole()
	mockConsole.SetNoPromptMode(true)

	subscriptionManager.
		On("GetSubscriptions", mock.Anything).
		Return([]account.Subscription{
			{Id: "sub-1", TenantId: "tenant-1", Name: "My Only Sub"},
		}, nil)

	ps := NewPromptService(
		authManager,
		mockConsole,
		ucm,
		subscriptionManager,
		resourceService,
		&internal.GlobalCommandOptions{NoPrompt: true},
	)

	result, err := ps.PromptSubscription(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "sub-1", result.Id)
	require.Len(t, mockConsole.Output(), 1)
	require.Equal(t, "Auto-selected subscription: My Only Sub", mockConsole.Output()[0])
}

func TestPromptSubscription_NoPrompt_DefaultNotFound_DemoModeRedactsId(t *testing.T) {
	t.Setenv("AZD_DEMO_MODE", "true")

	cfg := config.NewEmptyConfig()
	err := cfg.Set("defaults.subscription", "sub-secret")
	require.NoError(t, err)

	ucm := newInMemoryUserConfigManager(cfg)
	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}
	mockConsole := mockinput.NewMockConsole()
	mockConsole.SetNoPromptMode(true)

	subscriptionManager.
		On("GetSubscriptions", mock.Anything).
		Return([]account.Subscription{
			{Id: "sub-1", TenantId: "tenant-1", Name: "Sub 1"},
		}, nil)

	ps := NewPromptService(
		authManager,
		mockConsole,
		ucm,
		subscriptionManager,
		resourceService,
		&internal.GlobalCommandOptions{NoPrompt: true},
	)

	_, err = ps.PromptSubscription(context.Background(), nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "default subscription not found")
	require.False(t, strings.Contains(err.Error(), "sub-secret"))
}

func TestPromptLocation_NoPrompt_FiltersAllowedValues(t *testing.T) {
	cfg := config.NewEmptyConfig()
	err := cfg.Set("defaults.location", "westus3")
	require.NoError(t, err)

	ucm := newInMemoryUserConfigManager(cfg)
	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}
	mockConsole := mockinput.NewMockConsole()
	mockConsole.SetNoPromptMode(true)

	subscriptionManager.
		On("GetLocations", mock.Anything, "sub-123").
		Return([]account.Location{
			{Name: "eastus2", DisplayName: "East US 2", RegionalDisplayName: "(US) East US 2"},
			{Name: "westus3", DisplayName: "West US 3", RegionalDisplayName: "(US) West US 3"},
		}, nil)

	ps := NewPromptService(
		authManager,
		mockConsole,
		ucm,
		subscriptionManager,
		resourceService,
		&internal.GlobalCommandOptions{NoPrompt: true},
	)

	location, err := ps.PromptLocation(context.Background(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-123"},
	}, &SelectOptions{
		AllowedValues: []string{"westus3"},
	})

	require.NoError(t, err)
	require.Equal(t, "westus3", location.Name)
	subscriptionManager.AssertExpectations(t)
}

func TestPromptLocation_NoPrompt_DefaultFilteredOut(t *testing.T) {
	cfg := config.NewEmptyConfig()
	err := cfg.Set("defaults.location", "westus3")
	require.NoError(t, err)

	ucm := newInMemoryUserConfigManager(cfg)
	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}
	mockConsole := mockinput.NewMockConsole()
	mockConsole.SetNoPromptMode(true)

	subscriptionManager.
		On("GetLocations", mock.Anything, "sub-123").
		Return([]account.Location{
			{Name: "eastus2", DisplayName: "East US 2", RegionalDisplayName: "(US) East US 2"},
			{Name: "westus3", DisplayName: "West US 3", RegionalDisplayName: "(US) West US 3"},
		}, nil)

	ps := NewPromptService(
		authManager,
		mockConsole,
		ucm,
		subscriptionManager,
		resourceService,
		&internal.GlobalCommandOptions{NoPrompt: true},
	)

	_, err = ps.PromptLocation(context.Background(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-123"},
	}, &SelectOptions{
		AllowedValues: []string{"eastus2"},
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "default location 'westus3' not found in the available location options")
	subscriptionManager.AssertExpectations(t)
}
