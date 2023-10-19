package remote

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// EnvNameEnvVarName is the name of the key used to store the envname property in the environment.
const EnvNameEnvVarName = "AZURE_ENV_NAME"

// LocationEnvVarName is the name of the key used to store the location property in the environment.
const LocationEnvVarName = "AZURE_LOCATION"

// SubscriptionIdEnvVarName is the name of they key used to store the subscription id property in the environment.
const SubscriptionIdEnvVarName = "AZURE_SUBSCRIPTION_ID"

type LocationFilterPredicate func(loc account.Location) bool

type Environment interface {
	PromptSubscription(ctx context.Context, msg string) (subscriptionId string, err error)
	PromptLocation(ctx context.Context, subId string, msg string, filter LocationFilterPredicate) (string, error)
	PromptResourceGroup(ctx context.Context, subId string) (string, error)
}

type DefaultEnvironment struct {
	console        input.Console
	accountManager account.Manager
	azCli          azcli.AzCli
}

func NewDefaultEnvironment(
	console input.Console,
	accountManager account.Manager,
	azCli azcli.AzCli,
) Environment {
	return &DefaultEnvironment{
		console:        console,
		accountManager: accountManager,
		azCli:          azCli,
	}
}

func (e *DefaultEnvironment) PromptSubscription(ctx context.Context, msg string) (subscriptionId string, err error) {
	subscriptionOptions, defaultSubscription, err := e.getSubscriptionOptions(ctx)
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
		subscriptionSelectionIndex, err := e.console.Select(ctx, input.ConsoleOptions{
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

	if !e.accountManager.HasDefaultSubscription() {
		if _, err := e.accountManager.SetDefaultSubscription(ctx, subscriptionId); err != nil {
			log.Printf("failed setting default subscription. %s\n", err.Error())
		}
	}

	return subscriptionId, nil
}

func (e *DefaultEnvironment) PromptLocation(
	ctx context.Context,
	subId string,
	msg string,
	filter LocationFilterPredicate,
) (string, error) {
	loc, err := promptLocationWithFilter(ctx, subId, msg, "", e.console, e.accountManager, filter)
	if err != nil {
		return "", err
	}

	if !e.accountManager.HasDefaultLocation() {
		if _, err := e.accountManager.SetDefaultLocation(ctx, subId, loc); err != nil {
			log.Printf("failed setting default location. %s\n", err.Error())
		}
	}

	return loc, nil
}

func (e *DefaultEnvironment) PromptResourceGroup(ctx context.Context, subId string) (string, error) {
	// Get current resource groups
	groups, err := e.azCli.ListResourceGroup(ctx, subId, nil)
	if err != nil {
		return "", fmt.Errorf("listing resource groups: %w", err)
	}

	slices.SortFunc(groups, func(a, b azcli.AzCliResource) int {
		return strings.Compare(a.Name, b.Name)
	})

	choices := make([]string, len(groups)+1)
	for idx, group := range groups {
		choices[idx] = fmt.Sprintf("%d. %s", idx, group.Name)
	}

	choice, err := e.console.Select(ctx, input.ConsoleOptions{
		Message: "Pick a resource group to use:",
		Options: choices,
	})
	if err != nil {
		return "", fmt.Errorf("selecting resource group: %w", err)
	}

	return groups[choice].Name, nil
}

func (p *DefaultEnvironment) getSubscriptionOptions(ctx context.Context) ([]string, any, error) {
	subscriptionInfos, err := p.accountManager.GetSubscriptions(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("listing accounts: %w", err)
	}

	// The default value is based on AZURE_SUBSCRIPTION_ID, falling back to whatever default subscription in
	// set in azd's config.
	defaultSubscriptionId := os.Getenv(SubscriptionIdEnvVarName)
	if defaultSubscriptionId == "" {
		defaultSubscriptionId = p.accountManager.GetDefaultSubscriptionID(ctx)
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

func promptLocationWithFilter(
	ctx context.Context,
	subscriptionId string,
	message string,
	help string,
	console input.Console,
	accountManager account.Manager,
	shouldDisplay func(account.Location) bool,
) (string, error) {
	allLocations, err := accountManager.GetLocations(ctx, subscriptionId)
	if err != nil {
		return "", fmt.Errorf("listing locations: %w", err)
	}

	locations := make([]account.Location, 0, len(allLocations))

	for _, location := range allLocations {
		if shouldDisplay(location) {
			locations = append(locations, location)
		}
	}

	slices.SortFunc(locations, func(a, b account.Location) int {
		return strings.Compare(
			strings.ToLower(a.RegionalDisplayName), strings.ToLower(b.RegionalDisplayName))
	})

	// Allow the environment variable `AZURE_LOCATION` to control the default value for the location
	// selection.
	defaultLocation := os.Getenv(LocationEnvVarName)

	// If no location is set in the process environment, see what the azd config default is.
	if defaultLocation == "" {
		defaultLocation = accountManager.GetDefaultLocationName(ctx)
	}

	var defaultOption any

	locationOptions := make([]string, len(locations))
	for index, location := range locations {
		locationOptions[index] = fmt.Sprintf("%2d. %s (%s)", index+1, location.RegionalDisplayName, location.Name)

		if strings.EqualFold(defaultLocation, location.Name) ||
			strings.EqualFold(defaultLocation, location.DisplayName) {
			defaultOption = locationOptions[index]
		}
	}

	selectedIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message:      message,
		Help:         help,
		Options:      locationOptions,
		DefaultValue: defaultOption,
	})

	if err != nil {
		return "", fmt.Errorf("prompting for location: %w", err)
	}

	return locations[selectedIndex].Name, nil
}
