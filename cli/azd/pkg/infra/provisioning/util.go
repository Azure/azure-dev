// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

// EnsureSubscriptionAndLocation ensures that a subscription and location are configured in the environment, prompting
// for values if they are not.
func EnsureSubscriptionAndLocation(ctx context.Context, env *environment.Environment, prompters Prompters) error {
	if env.GetSubscriptionId() == "" {
		subscriptionId, err := prompters.Subscription(ctx, "Please select an Azure Subscription to use:")
		if err != nil {
			return err
		}

		env.SetSubscriptionId(subscriptionId)
		telemetry.SetGlobalAttributes(fields.SubscriptionIdKey.String(env.GetSubscriptionId()))

		if err := env.Save(); err != nil {
			return err
		}
	}

	if env.GetLocation() == "" {
		location, err := prompters.Location(
			ctx,
			env.GetSubscriptionId(),
			"Please select an Azure location to use:",
			func(_ account.Location) bool { return true })
		if err != nil {
			return err
		}

		env.SetLocation(location)

		if err := env.Save(); err != nil {
			return err
		}
	}

	return nil
}

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
