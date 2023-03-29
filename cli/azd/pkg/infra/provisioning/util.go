// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

func getSubscriptionOptions(ctx context.Context, subscriptions account.Manager) ([]string, any, error) {
	subscriptionInfos, err := subscriptions.GetSubscriptions(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("listing accounts: %w", err)
	}

	// The default value is based on AZURE_SUBSCRIPTION_ID, falling back to whatever default subscription in
	// set in azd's config.
	defaultSubscriptionId := os.Getenv(environment.SubscriptionIdEnvVarName)
	if defaultSubscriptionId == "" {
		defaultSubscriptionId = subscriptions.GetDefaultSubscriptionID(ctx)
	}

	var subscriptionOptions = make([]string, len(subscriptionInfos))
	var defaultSubscription any

	for index, info := range subscriptionInfos {
		subscriptionOptions[index] = fmt.Sprintf("%2d. %s (%s)", index+1, info.Name, info.Id)

		if info.Id == defaultSubscriptionId {
			defaultSubscription = subscriptionOptions[index]
		}
	}

	return subscriptionOptions, defaultSubscription, nil
}
