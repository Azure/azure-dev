// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockauth"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_PromptService_PromptSubscription(t *testing.T) {
	//mockContext := mocks.NewMockContext(context.Background())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	userConfigManager := config.NewUserConfigManager(fileConfigManager)

	authManager := &mockauth.MockAuthManager{}
	subscriptionService := &mockaccount.MockSubscriptionService{}
	resourceService := &mockazapi.MockResourceService{}

	tokenClaims := auth.TokenClaims{
		TenantId: "tenant-1",
	}

	authManager.
		On("ClaimsForCurrentUser", mock.Anything, mock.Anything).
		Return(tokenClaims, nil)

	subscriptionService.
		On("ListSubscriptions", mock.Anything, tokenClaims.TenantId).
		Return([]*armsubscriptions.Subscription{
			{
				ID:             to.Ptr("/subscriptions/subscription-1"),
				SubscriptionID: to.Ptr("subscription-1"),
				TenantID:       to.Ptr("tenant-1"),
				DisplayName:    to.Ptr("Subscription 1"),
			},
			{
				ID:             to.Ptr("/subscriptions/subscription-2"),
				SubscriptionID: to.Ptr("subscription-2"),
				TenantID:       to.Ptr("tenant-1"),
				DisplayName:    to.Ptr("Subscription 2"),
			},
		}, nil)

	promptService := NewPromptService(authManager, userConfigManager, subscriptionService, resourceService)
	require.NotNil(t, promptService)

	// selected, err := promptService.PromptSubscription(*mockContext.Context, nil)
	// require.NoError(t, err)
	// require.NotNil(t, selected)
}
