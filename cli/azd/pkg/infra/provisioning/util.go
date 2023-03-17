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

const (
	manualSubscriptionEntryOption = "Other (enter manually)"
)

func getSubscriptionOptions(ctx context.Context, subscriptions account.Manager) ([]string, any, error) {
	subscriptionInfos, err := subscriptions.GetSubscriptions(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("listing accounts: %w", err)
	}

	// If `AZURE_SUBSCRIPTION_ID` is set in the environment, use it to influence
	// the default option in our prompt. Fall back to the what the `az` CLI is
	// configured to use if the environment variable is unset.
	defaultSubscriptionId := os.Getenv(environment.SubscriptionIdEnvVarName)
	if defaultSubscriptionId == "" {
		defaultSubscriptionId = subscriptions.GetDefaultSubscriptionID(ctx)
	}

	var subscriptionOptions = make([]string, len(subscriptionInfos)+1)
	var defaultSubscription any

	for index, info := range subscriptionInfos {
		subscriptionOptions[index] = fmt.Sprintf("%2d. %s (%s)", index+1, info.Name, info.Id)

		if info.Id == defaultSubscriptionId {
			defaultSubscription = subscriptionOptions[index]
		}
	}

	subscriptionOptions[len(subscriptionOptions)-1] = manualSubscriptionEntryOption
	return subscriptionOptions, defaultSubscription, nil
}
