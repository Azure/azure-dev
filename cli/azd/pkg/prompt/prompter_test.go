// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/stretchr/testify/require"
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
