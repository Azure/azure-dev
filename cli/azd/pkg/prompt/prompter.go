// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/pkg/account"
	"github.com/azure/azure-dev/pkg/azapi"
	"github.com/azure/azure-dev/pkg/azure"
	"github.com/azure/azure-dev/pkg/azureutil"
	"github.com/azure/azure-dev/pkg/cloud"
	"github.com/azure/azure-dev/pkg/environment"
	"github.com/azure/azure-dev/pkg/input"
	"github.com/azure/azure-dev/pkg/stringutil"
)

type LocationFilterPredicate func(loc account.Location) bool

type Prompter interface {
	PromptSubscription(ctx context.Context, msg string) (subscriptionId string, err error)
	PromptLocation(
		ctx context.Context,
		subId string,
		msg string,
		filter LocationFilterPredicate,
		defaultLocation *string) (string, error)
	PromptResourceGroup(ctx context.Context, options PromptResourceOptions) (string, error)
	PromptResourceGroupFrom(
		ctx context.Context, subscriptionId string, location string, options PromptResourceGroupFromOptions) (string, error)
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
			log.Printf("failed setting default subscription. %v\n", err)
		}
	}

	return subscriptionId, nil
}

func (p *DefaultPrompter) PromptLocation(
	ctx context.Context,
	subId string,
	msg string,
	filter LocationFilterPredicate,
	defaultLocation *string,
) (string, error) {
	loc, err := azureutil.PromptLocationWithFilter(ctx, subId, msg, "", p.console, p.accountManager, filter, defaultLocation)
	if err != nil {
		return "", err
	}

	if !p.accountManager.HasDefaultLocation() {
		if _, err := p.accountManager.SetDefaultLocation(ctx, subId, loc); err != nil {
			log.Printf("failed setting default location. %v\n", err)
		}
	}

	return loc, nil
}

type PromptResourceOptions struct {
	DisableCreateNew bool
}

func (p *DefaultPrompter) PromptResourceGroup(ctx context.Context, options PromptResourceOptions) (string, error) {
	return p.PromptResourceGroupFrom(
		ctx,
		p.env.GetSubscriptionId(),
		p.env.GetLocation(),
		PromptResourceGroupFromOptions{
			Tags: map[string]string{
				azure.TagKeyAzdEnvName: p.env.Name(),
			},
			DefaultName:      fmt.Sprintf("rg-%s", p.env.Name()),
			DisableCreateNew: options.DisableCreateNew,
		})
}

type PromptResourceGroupFromOptions struct {
	Tags                  map[string]string
	DefaultName           string
	NewResourceGroupHelp  string
	PickResourceGroupHelp string
	DisableCreateNew      bool
}

func (p *DefaultPrompter) PromptResourceGroupFrom(
	ctx context.Context, subscriptionId string, location string, options PromptResourceGroupFromOptions) (string, error) {
	// Get current resource groups
	groups, err := p.resourceService.ListResourceGroup(ctx, subscriptionId, nil)
	if err != nil {
		return "", fmt.Errorf("listing resource groups: %w", err)
	}

	slices.SortFunc(groups, func(a, b *azapi.Resource) int {
		return stringutil.CompareLower(a.Name, b.Name)
	})

	canCreateNeResourceGroup := !options.DisableCreateNew

	choices := make([]string, len(groups))
	canCreateOverride := 0
	if canCreateNeResourceGroup {
		choices = make([]string, len(groups)+1)
		choices[0] = "1. Create a new resource group"
		canCreateOverride = 1
	}
	for idx, group := range groups {
		choices[idx+canCreateOverride] = fmt.Sprintf("%d. %s", idx+canCreateOverride+1, group.Name)
	}

	choice, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Pick a resource group to use:",
		Options: choices,
		Help:    options.PickResourceGroupHelp,
	})
	if err != nil {
		return "", fmt.Errorf("selecting resource group: %w", err)
	}

	if !canCreateNeResourceGroup {
		return groups[choice].Name, nil
	}

	if choice > 0 {
		return groups[choice-1].Name, nil
	}

	if location == "" {
		loc, err := p.PromptLocation(ctx, subscriptionId, "Select a location to create the resource group in:", nil, nil)
		if err != nil {
			return "", fmt.Errorf("prompting for location: %w", err)
		}
		location = loc
	}

	name, err := p.console.Prompt(ctx, input.ConsoleOptions{
		Message:      "Enter a name for the new resource group:",
		DefaultValue: options.DefaultName,
		Help:         options.NewResourceGroupHelp,
	})
	if err != nil {
		return "", fmt.Errorf("prompting for resource group name: %w", err)
	}

	tagsParam := make(map[string]*string, len(options.Tags))
	for k, v := range options.Tags {
		tagsParam[k] = to.Ptr(v)
	}

	_, err = p.resourceService.CreateOrUpdateResourceGroup(ctx, subscriptionId, name, location, tagsParam)
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

	slices.SortFunc(subscriptionInfos, func(a, b account.Subscription) int {
		return stringutil.CompareLower(a.Name, b.Name)
	})

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
