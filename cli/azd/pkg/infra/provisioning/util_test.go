// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/stretchr/testify/require"
)

func Test_getSubscriptionOptions(t *testing.T) {
	t.Run("no default config set", func(t *testing.T) {
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

		subList, result, err := getSubscriptionOptions(context.Background(), mockAccount)

		require.Nil(t, err)
		require.EqualValues(t, 1, len(subList))
		require.EqualValues(t, nil, result)
	})

	t.Run("default value set", func(t *testing.T) {
		// mocked config
		defaultSubId := "SUBSCRIPTION_DEFAULT"
		ctx := context.Background()
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

		subList, result, err := getSubscriptionOptions(ctx, mockAccount)

		require.Nil(t, err)
		require.EqualValues(t, 2, len(subList))
		require.NotNil(t, result)
		defSub, ok := result.(string)
		require.True(t, ok)
		require.EqualValues(t, " 1. DISPLAY DEFAULT (SUBSCRIPTION_DEFAULT)", defSub)
	})
}
