package prompt

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

type Prompters struct {
	console        input.Console
	env            *environment.Environment
	accountManager account.Manager
}

// EnsureEnv ensures that the environment is in a provision-ready state with required values set, prompting the user if
// values are unset.
//
// This currently means that subscription (AZURE_SUBSCRIPTION_ID) and location (AZURE_LOCATION) variables are set.
func (p *Prompters) PromptAll(ctx context.Context) error {
	if p.env.GetSubscriptionId() == "" {
		subscriptionId, err := p.PromptSubscription(ctx, "Please select an Azure Subscription to use:")
		if err != nil {
			return err
		}

		p.env.SetSubscriptionId(subscriptionId)

		if err := p.env.Save(); err != nil {
			return err
		}
	}

	if p.env.GetLocation() == "" {
		location, err := p.PromptLocation(
			ctx,
			p.env.GetSubscriptionId(),
			"Select an Azure location to use:",
			func(_ account.Location) bool { return true },
		)
		if err != nil {
			return err
		}

		p.env.SetLocation(location)

		if err := p.env.Save(); err != nil {
			return err
		}
	}

	return nil
}

func (p *Prompters) PromptSubscription(ctx context.Context, msg string) (subscriptionId string, err error) {
	subscriptionOptions, defaultSubscription, err := p.getSubscriptionOptions(ctx)
	if err != nil {
		return "", err
	}

	if len(subscriptionOptions) == 0 {
		return "", fmt.Errorf(heredoc.Doc(
			`no subscriptions found.
			Ensure you have a subscription by visiting https://portal.azure.com and search for Subscriptions in the search bar.
			Once you have a subscription, run 'azd auth login' again to reload subscriptions.`))
	}

	for subscriptionId == "" {
		subscriptionSelectionIndex, err := p.console.Select(ctx, input.ConsoleOptions{
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

	if !p.accountManager.HasDefaultSubscription() {
		if _, err := account.SetDefaultSubscription(ctx, subscriptionId); err != nil {
			log.Printf("failed setting default subscription. %s\n", err.Error())
		}
	}

	return subscriptionId, nil
}

func (p *Prompters) PromptLocation(ctx context.Context, subId string, msg string, filter func(loc account.Location) bool,
) (string, error) {
	loc, err := azureutil.PromptLocationWithFilter(ctx, subId, msg, "", p.console, p.accountManager, filter)
	if err != nil {
		return "", err
	}

	if !p.accountManager.HasDefaultLocation() {
		if _, err := account.SetDefaultLocation(ctx, subId, loc); err != nil {
			log.Printf("failed setting default location. %s\n", err.Error())
		}
	}

	return loc, nil
}

func (m *Prompters) getSubscriptionOptions(ctx context.Context) ([]string, any, error) {
	subscriptionInfos, err := m.accountManager.GetSubscriptions(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("listing accounts: %w", err)
	}

	// The default value is based on AZURE_SUBSCRIPTION_ID, falling back to whatever default subscription in
	// set in azd's config.
	defaultSubscriptionId := os.Getenv(environment.SubscriptionIdEnvVarName)
	if defaultSubscriptionId == "" {
		defaultSubscriptionId = m.accountManager.GetDefaultSubscriptionID(ctx)
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
