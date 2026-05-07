// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"testing"

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

	promptService := NewPromptService(authManager, mockConsole, ucm, subscriptionManager, resourceService)
	require.NotNil(t, promptService)
}

func TestFormatSubscriptionDisplayName_DemoModeHidesId(t *testing.T) {
	displayName := formatSubscriptionDisplayName(&account.Subscription{
		Id:   "/subscriptions/sub-1",
		Name: "Subscription 1",
	}, true)

	require.Equal(t, "Subscription 1", displayName)
}
