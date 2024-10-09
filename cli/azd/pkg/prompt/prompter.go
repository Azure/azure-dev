package prompt

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

type LocationFilterPredicate func(loc account.Location) bool

type Prompter interface {
	PromptSubscription(ctx context.Context, msg string) (subscriptionId string, err error)
	PromptLocation(ctx context.Context, subId string, msg string, filter LocationFilterPredicate) (string, error)
	PromptResourceGroup(ctx context.Context) (string, error)
	PromptResource(ctx context.Context, options PromptResourceOptions) (*azapi.Resource, error)
}

type DefaultPrompter struct {
	console         input.Console
	env             *environment.Environment
	accountManager  account.Manager
	resourceService *azapi.ResourceService
	portalUrlBase   string
}

func NewDefaultPrompter(
	env *environment.Environment,
	console input.Console,
	accountManager account.Manager,
	resourceService *azapi.ResourceService,
	cloud *cloud.Cloud,
) Prompter {
	return &DefaultPrompter{
		console:         console,
		env:             env,
		accountManager:  accountManager,
		resourceService: resourceService,
		portalUrlBase:   cloud.PortalUrlBase,
	}
}

func (p *DefaultPrompter) PromptSubscription(ctx context.Context, msg string) (subscriptionId string, err error) {
	subscriptionOptions, subscriptions, defaultSubscription, err := p.getSubscriptionOptions(ctx)
	if err != nil {
		return "", err
	}

	if len(subscriptionOptions) == 0 {
		return "", errors.New(heredoc.Docf(
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
	groups, err := p.resourceService.ListResourceGroup(ctx, p.env.GetSubscriptionId(), nil)
	if err != nil {
		return "", fmt.Errorf("listing resource groups: %w", err)
	}

	slices.SortFunc(groups, func(a, b *azapi.Resource) int {
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

	err = p.resourceService.CreateOrUpdateResourceGroup(ctx, p.env.GetSubscriptionId(), name, p.env.GetLocation(),
		map[string]*string{
			azure.TagKeyAzdEnvName: to.Ptr(p.env.Name()),
		},
	)
	if err != nil {
		return "", fmt.Errorf("creating resource group: %w", err)
	}

	return name, nil
}

type PromptResourceOptions struct {
	ResourceType string
	DisplayName  string
	Description  string
}

// PromptResource prompts the user to select a resource with the specified resource type.
// If the user selects to create a new resource, the user will be prompted to enter a name for the new resource.
// This new resource is intended to be created in the Bicep deployment
func (p *DefaultPrompter) PromptResource(ctx context.Context, options PromptResourceOptions) (*azapi.Resource, error) {
	resourceListOptions := armresources.ClientListOptions{
		Filter: to.Ptr(fmt.Sprintf("resourceType eq '%s'", options.ResourceType)),
	}

	if options.DisplayName == "" {
		options.DisplayName = filepath.Base(options.ResourceType)
	}

	resourceTypeDisplayName := strings.ToLower(options.DisplayName)

	resources, err := p.resourceService.ListSubscriptionResources(ctx, p.env.GetSubscriptionId(), &resourceListOptions)
	if err != nil {
		return nil, fmt.Errorf("listing subscription resources: %w", err)
	}

	slices.SortFunc(resources, func(a, b *azapi.Resource) int {
		return strings.Compare(a.Name, b.Name)
	})

	choices := make([]string, len(resources)+1)
	choices[0] = fmt.Sprintf("Create a new %s", resourceTypeDisplayName)

	for idx, resource := range resources {
		parsedResource, err := arm.ParseResourceID(*&resource.Id)
		if err != nil {
			return nil, fmt.Errorf("parsing resource id: %w", err)
		}

		choices[idx+1] = fmt.Sprintf("%d. %s (Resource Group: %s)", idx+1, resource.Name, parsedResource.ResourceGroupName)
	}

	selectedIndex, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf("Select a %s to use:", resourceTypeDisplayName),
		Options: choices,
		Help:    options.Description,
	})
	if err != nil {
		return nil, fmt.Errorf("selecting %s: %w", resourceTypeDisplayName, err)
	}

	if selectedIndex > 0 {
		return resources[selectedIndex-1], nil
	}

	name, err := p.console.Prompt(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf("Enter a name for the new %s:", resourceTypeDisplayName),
	})
	if err != nil {
		return nil, fmt.Errorf("prompting for %s name: %w", resourceTypeDisplayName, err)
	}

	return &azapi.Resource{
		Name: name,
	}, nil
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
