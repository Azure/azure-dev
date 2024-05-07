package prompt

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type LocationFilterPredicate func(loc account.Location) bool

type Prompter interface {
	PromptSubscription(ctx context.Context, msg string) (subscriptionId string, err error)
	PromptLocation(ctx context.Context, subId string, msg string, filter LocationFilterPredicate) (string, error)
	PromptResourceGroup(ctx context.Context) (string, error)
}

type DefaultPrompter struct {
	console        input.Console
	env            *environment.Environment
	accountManager account.Manager
	azCli          azcli.AzCli
	portalUrlBase  string
}

func NewDefaultPrompter(
	env *environment.Environment,
	console input.Console,
	accountManager account.Manager,
	azCli azcli.AzCli,
	portalUrlBase cloud.PortalUrlBase,
) Prompter {
	return &DefaultPrompter{
		console:        console,
		env:            env,
		accountManager: accountManager,
		azCli:          azCli,
		portalUrlBase:  portalUrlBase,
	}
}

func (p *DefaultPrompter) PromptSubscription(ctx context.Context, msg string) (subscriptionId string, err error) {
	subscriptionOptions, subscriptions, defaultSubscription, err := p.getSubscriptionOptions(ctx)
	if err != nil {
		return "", err
	}

	if len(subscriptionOptions) == 0 {
		return "", fmt.Errorf(heredoc.Docf(
			`no subscriptions found.
			Ensure you have a subscription by visiting %s and search for Subscriptions in the search bar.
			Once you have a subscription, run 'azd auth login' again to reload subscriptions.`,
			p.portalUrlBase,
		))
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

		subscriptionId = subscriptions[subscriptionSelectionIndex]
	}

	if !p.accountManager.HasDefaultSubscription() {
		if _, err := p.accountManager.SetDefaultSubscription(ctx, subscriptionId); err != nil {
			log.Printf("failed setting default subscription. %s\n", err.Error())
		}
	}

	return subscriptionId, nil
}

func (p *DefaultPrompter) PromptLocation(
	ctx context.Context,
	subId string,
	msg string,
	filter LocationFilterPredicate,
) (string, error) {
	loc, err := azureutil.PromptLocationWithFilter(ctx, subId, msg, "", p.console, p.accountManager, filter)
	if err != nil {
		return "", err
	}

	if !p.accountManager.HasDefaultLocation() {
		if _, err := p.accountManager.SetDefaultLocation(ctx, subId, loc); err != nil {
			log.Printf("failed setting default location. %s\n", err.Error())
		}
	}

	return loc, nil
}

func (p *DefaultPrompter) PromptResourceGroup(ctx context.Context) (string, error) {
	// Get current resource groups
	groups, err := p.azCli.ListResourceGroup(ctx, p.env.GetSubscriptionId(), nil)
	if err != nil {
		return "", fmt.Errorf("listing resource groups: %w", err)
	}

	slices.SortFunc(groups, func(a, b azcli.AzCliResource) int {
		return strings.Compare(a.Name, b.Name)
	})

	choices := make([]string, len(groups)+1)
	choices[0] = "Create a new resource group"
	for idx, group := range groups {
		choices[idx+1] = fmt.Sprintf("%d. %s", idx+1, group.Name)
	}

	choice, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Pick a resource group to use:",
		Options: choices,
	})
	if err != nil {
		return "", fmt.Errorf("selecting resource group: %w", err)
	}

	if choice > 0 {
		return groups[choice-1].Name, nil
	}

	name, err := p.console.Prompt(ctx, input.ConsoleOptions{
		Message:      "Enter a name for the new resource group:",
		DefaultValue: fmt.Sprintf("rg-%s", p.env.Name()),
	})
	if err != nil {
		return "", fmt.Errorf("prompting for resource group name: %w", err)
	}

	err = p.azCli.CreateOrUpdateResourceGroup(ctx, p.env.GetSubscriptionId(), name, p.env.GetLocation(),
		map[string]*string{
			azure.TagKeyAzdEnvName: to.Ptr(p.env.Name()),
		},
	)
	if err != nil {
		return "", fmt.Errorf("creating resource group: %w", err)
	}

	return name, nil
}

func (p *DefaultPrompter) getSubscriptionOptions(ctx context.Context) ([]string, []string, any, error) {
	subscriptionInfos, err := p.accountManager.GetSubscriptions(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("listing accounts: %w", err)
	}

	// The default value is based on AZURE_SUBSCRIPTION_ID, falling back to whatever default subscription in
	// set in azd's config.
	defaultSubscriptionId := os.Getenv(environment.SubscriptionIdEnvVarName)
	if defaultSubscriptionId == "" {
		defaultSubscriptionId = p.accountManager.GetDefaultSubscriptionID(ctx)
	}

	var subscriptionOptions = make([]string, len(subscriptionInfos))
	var subscriptions = make([]string, len(subscriptionInfos))
	var defaultSubscription any

	for index, info := range subscriptionInfos {
		if v, err := strconv.ParseBool(os.Getenv("AZD_DEMO_MODE")); err == nil && v {
			subscriptionOptions[index] = fmt.Sprintf("%2d. %s", index+1, info.Name)
		} else {
			subscriptionOptions[index] = fmt.Sprintf("%2d. %s (%s)", index+1, info.Name, info.Id)
		}

		subscriptions[index] = info.Id

		if info.Id == defaultSubscriptionId {
			defaultSubscription = subscriptionOptions[index]
		}
	}

	return subscriptionOptions, subscriptions, defaultSubscription, nil
}
