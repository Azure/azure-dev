// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockauth"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_PromptService_PromptSubscription(t *testing.T) {
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	userConfigManager := config.NewUserConfigManager(fileConfigManager)

	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}

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

	promptService := NewPromptService(authManager, userConfigManager, subscriptionManager, resourceService, globalOptions)
	require.NotNil(t, promptService)
}
