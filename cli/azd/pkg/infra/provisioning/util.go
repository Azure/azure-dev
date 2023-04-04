// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// EnsureSubscriptionAndLocation ensures that a subscription and location are configured in the environment, prompting
// for values if they are not.
func EnsureSubscriptionAndLocation(
	ctx context.Context,
	console input.Console,
	env *environment.Environment,
	accountManager account.Manager) error {
	if env.GetSubscriptionId() == "" {
		subscriptionId, err := promptSubscription(
			ctx,
			"Please select an Azure Subscription to use:",
			console,
			env,
			accountManager)
		if err != nil {
			return err
		}

		env.SetSubscriptionId(subscriptionId)

		if err := env.Save(); err != nil {
			return err
		}
	}

	if env.GetLocation() == "" {
		location, err := promptLocation(
			ctx,
			env.GetSubscriptionId(),
			"Please select an Azure location to use:",
			func(_ account.Location) bool { return true },
			console,
			env,
			accountManager)
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

func promptSubscription(
	ctx context.Context,
	msg string,
	console input.Console,
	env *environment.Environment,
	account account.Manager) (subscriptionId string, err error) {
	subscriptionOptions, defaultSubscription, err := getSubscriptionOptions(ctx, account)
	if err != nil {
		return "", err
	}

	for subscriptionId == "" {
		subscriptionSelectionIndex, err := console.Select(ctx, input.ConsoleOptions{
			Message:      msg,
			Options:      subscriptionOptions,
			DefaultValue: defaultSubscription,
		})

		if err != nil {
			return "", fmt.Errorf("reading subscription id: %w", err)
		}

		subscriptionSelection := subscriptionOptions[subscriptionSelectionIndex]
		subscriptionId = subscriptionSelection[len(subscriptionSelection)-
			len("(00000000-0000-0000-0000-000000000000)")+1 : len(subscriptionSelection)-1]
	}

	if !account.HasDefaultSubscription() {
		if _, err := account.SetDefaultSubscription(ctx, env.GetSubscriptionId()); err != nil {
			log.Printf("failed setting default subscription. %s\n", err.Error())
		}
	}

	return subscriptionId, nil
}

func promptLocation(
	ctx context.Context,
	subId string,
	msg string,
	filter func(loc account.Location) bool,
	console input.Console,
	env *environment.Environment,
	account account.Manager,
) (string, error) {
	loc, err := azureutil.PromptLocationWithFilter(ctx, subId, msg, "", console, account, filter)
	if err != nil {
		return "", err
	}

	if !account.HasDefaultLocation() {
		if _, err := account.SetDefaultLocation(ctx, env.GetSubscriptionId(), env.GetLocation()); err != nil {
			log.Printf("failed setting default location. %s\n", err.Error())
		}
	}

	return loc, nil
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
