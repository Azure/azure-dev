// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/stretchr/testify/require"
)

func Test_getSubscriptionOptions(t *testing.T) {
	t.Run("no default config set", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.New("test")
		azCli := mockazcli.NewAzCliFromMockContext(mockContext)
		mockAccount := &mockaccount.MockAccountManager{
			Subscriptions: []account.Subscription{
				{
					Id:                 "1",
					Name:               "sub1",
					TenantId:           "",
					UserAccessTenantId: "",
					IsDefault:          false,
				},
			},
		}

		prompter := NewDefaultPrompter(
			env,
			mockContext.Console,
			mockAccount,
			azCli,
			cloud.AzurePublic().PortalUrlBase,
		).(*DefaultPrompter)
		subList, subs, result, err := prompter.getSubscriptionOptions(*mockContext.Context)

		require.Nil(t, err)
		require.EqualValues(t, 1, len(subList))
		require.EqualValues(t, 1, len(subs))
		require.EqualValues(t, nil, result)
	})

	t.Run("default value set", func(t *testing.T) {
		// mocked config
		defaultSubId := "SUBSCRIPTION_DEFAULT"
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.New("test")
		azCli := mockazcli.NewAzCliFromMockContext(mockContext)
		mockAccount := &mockaccount.MockAccountManager{
			DefaultLocation:     "location",
			DefaultSubscription: defaultSubId,
			Subscriptions: []account.Subscription{
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
			},
			Locations: []account.Location{},
		}

		prompter := NewDefaultPrompter(
			env,
			mockContext.Console,
			mockAccount,
			azCli,
			cloud.AzurePublic().PortalUrlBase,
		).(*DefaultPrompter)
		subList, subs, result, err := prompter.getSubscriptionOptions(*mockContext.Context)

		require.Nil(t, err)
		require.EqualValues(t, 2, len(subList))
		require.EqualValues(t, 2, len(subs))
		require.NotNil(t, result)
		defSub, ok := result.(string)
		require.True(t, ok)
		require.EqualValues(t, " 1. DISPLAY DEFAULT (SUBSCRIPTION_DEFAULT)", defSub)
	})
}
