// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
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
