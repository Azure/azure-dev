// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

// subFilterActions registers the "azd config sub-filter" command group.
func subFilterActions(
	configGroup *actions.ActionDescriptor,
) *actions.ActionDescriptor {
	group := configGroup.Add("sub-filter", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "sub-filter",
			Short: "Manage subscription filters for tenant-scoped subscription prompts.",
			Long: "Manage per-tenant subscription filters that control which " +
				"subscriptions are shown during interactive prompts.\n" +
				"Filters are stored locally in your user configuration " +
				"and apply per-device.",
		},
	})

	group.Add("set", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Set a subscription filter for a tenant.",
			Long: "Select which subscriptions to include when prompted " +
				"for a subscription under a specific tenant.\n" +
				"If a filter already exists, the previously selected " +
				"subscriptions are pre-checked.",
		},
		ActionResolver: newSubFilterSetAction,
	})

	group.Add("remove", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Remove a saved subscription filter for a tenant.",
			Long: "Remove the subscription filter for a tenant so that " +
				"all subscriptions are shown during prompts.",
		},
		ActionResolver: newSubFilterRemoveAction,
	})

	return group
}

// azd config sub-filter set

type subFilterSetAction struct {
	accountManager    account.Manager
	userConfigManager config.UserConfigManager
	console           input.Console
}

func newSubFilterSetAction(
	accountManager account.Manager,
	userConfigManager config.UserConfigManager,
	console input.Console,
) actions.Action {
	return &subFilterSetAction{
		accountManager:    accountManager,
		userConfigManager: userConfigManager,
		console:           console,
	}
}

func (a *subFilterSetAction) Run(
	ctx context.Context,
) (*actions.ActionResult, error) {
	// Load subscriptions
	var subscriptions []account.Subscription
	loadingSpinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Loading subscriptions...",
	})

	err := loadingSpinner.Run(ctx, func(ctx context.Context) error {
		var loadErr error
		subscriptions, loadErr = a.accountManager.GetSubscriptions(ctx)
		return loadErr
	})
	if err != nil {
		return nil, fmt.Errorf("listing subscriptions: %w", err)
	}

	if len(subscriptions) == 0 {
		return nil, fmt.Errorf("no subscriptions found")
	}

	// Resolve tenant
	tenantId, tenantName, err := a.resolveTenant(
		ctx, subscriptions,
	)
	if err != nil {
		return nil, err
	}

	// Filter subscriptions to the selected tenant
	tenantSubs := prompt.FilterSubscriptionsByTenantId(
		subscriptions, tenantId,
	)
	if len(tenantSubs) == 0 {
		return nil, fmt.Errorf(
			"no subscriptions found for tenant %s", tenantId,
		)
	}

	// Build options for multi-select
	hideId := prompt.IsDemoModeEnabled()
	displayFn := func(sub *account.Subscription) string {
		return prompt.FormatSubscriptionDisplay(sub, hideId)
	}

	options := make([]string, len(tenantSubs))
	for i := range tenantSubs {
		options[i] = displayFn(&tenantSubs[i])
	}

	// Load existing filter to pre-check items
	cfg, err := a.userConfigManager.Load()
	if err != nil {
		return nil, fmt.Errorf("loading user config: %w", err)
	}

	existingFilter, _ := prompt.LoadSubscriptionFilter(cfg, tenantId)
	preSelected := prompt.SubscriptionsMatchingFilter(
		tenantSubs, existingFilter, displayFn,
	)

	// Prompt multi-select
	selected, err := a.console.MultiSelect(ctx, input.ConsoleOptions{
		Message:      "Select subscriptions to include in the filter",
		Options:      options,
		DefaultValue: preSelected,
	})
	if err != nil {
		return nil, fmt.Errorf("selecting subscriptions: %w", err)
	}

	if len(selected) == 0 {
		a.console.Message(
			ctx,
			"No subscriptions selected. Filter not updated.",
		)
		return nil, nil
	}

	// Map selected display strings back to subscription IDs
	selectedIds := subscriptionIdsByDisplayName(
		tenantSubs, displayFn, selected,
	)

	// Save filter
	if err := prompt.SaveSubscriptionFilter(
		cfg, tenantId, selectedIds,
	); err != nil {
		return nil, fmt.Errorf("saving subscription filter: %w", err)
	}

	if err := a.userConfigManager.Save(cfg); err != nil {
		return nil, fmt.Errorf("saving user config: %w", err)
	}

	displayTenant := tenantName
	if displayTenant == "" {
		displayTenant = tenantId
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf(
				"Saved subscription filter for tenant %s"+
					" (%d subscription(s))",
				displayTenant,
				len(selectedIds),
			),
		},
	}, nil
}

func (a *subFilterSetAction) resolveTenant(
	ctx context.Context,
	subscriptions []account.Subscription,
) (string, string, error) {
	tenantNames, err := a.accountManager.GetTenantDisplayNames(ctx)
	if err != nil {
		tenantNames = nil
	}

	tenants := prompt.ExtractUniqueTenants(subscriptions, tenantNames)
	if len(tenants) == 0 {
		return "", "", fmt.Errorf("no tenants found")
	}

	if len(tenants) == 1 {
		return tenants[0].Id, tenants[0].DisplayName, nil
	}

	// Build options without "All tenants" — filter must be per-tenant
	options := make([]string, len(tenants))
	for i, t := range tenants {
		options[i] = prompt.FormatTenantDisplay(i+1, t)
	}

	selectedIndex, err := a.console.Select(ctx, input.ConsoleOptions{
		Message: "Select a tenant to set a subscription filter for",
		Options: options,
	})
	if err != nil {
		return "", "", fmt.Errorf("selecting tenant: %w", err)
	}

	return tenants[selectedIndex].Id,
		tenants[selectedIndex].DisplayName, nil
}

// azd config sub-filter remove

type subFilterRemoveAction struct {
	accountManager    account.Manager
	userConfigManager config.UserConfigManager
	console           input.Console
}

func newSubFilterRemoveAction(
	accountManager account.Manager,
	userConfigManager config.UserConfigManager,
	console input.Console,
) actions.Action {
	return &subFilterRemoveAction{
		accountManager:    accountManager,
		userConfigManager: userConfigManager,
		console:           console,
	}
}

func (a *subFilterRemoveAction) Run(
	ctx context.Context,
) (*actions.ActionResult, error) {
	// Load subscriptions for tenant resolution
	var subscriptions []account.Subscription
	loadingSpinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Loading subscriptions...",
	})

	err := loadingSpinner.Run(ctx, func(ctx context.Context) error {
		var loadErr error
		subscriptions, loadErr = a.accountManager.GetSubscriptions(ctx)
		return loadErr
	})
	if err != nil {
		return nil, fmt.Errorf("listing subscriptions: %w", err)
	}

	if len(subscriptions) == 0 {
		return nil, fmt.Errorf("no subscriptions found")
	}

	// Resolve tenant
	tenantId, tenantName, err := a.resolveTenant(
		ctx, subscriptions,
	)
	if err != nil {
		return nil, err
	}

	// Check if filter exists
	cfg, err := a.userConfigManager.Load()
	if err != nil {
		return nil, fmt.Errorf("loading user config: %w", err)
	}

	_, exists := prompt.LoadSubscriptionFilter(cfg, tenantId)
	if !exists {
		displayTenant := tenantName
		if displayTenant == "" {
			displayTenant = tenantId
		}
		a.console.Message(
			ctx,
			fmt.Sprintf(
				"No saved filter to remove for tenant %s.",
				displayTenant,
			),
		)
		return nil, nil
	}

	// Confirm removal
	displayTenant := tenantName
	if displayTenant == "" {
		displayTenant = tenantId
	}

	confirmed, err := a.console.Confirm(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf(
			"Remove subscription filter for tenant %s?",
			displayTenant,
		),
	})
	if err != nil {
		return nil, fmt.Errorf("confirming removal: %w", err)
	}

	if !confirmed {
		a.console.Message(ctx, "Filter removal cancelled.")
		return nil, nil
	}

	// Remove filter
	if err := prompt.RemoveSubscriptionFilter(cfg, tenantId); err != nil {
		return nil, fmt.Errorf(
			"removing subscription filter: %w", err,
		)
	}

	if err := a.userConfigManager.Save(cfg); err != nil {
		return nil, fmt.Errorf("saving user config: %w", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf(
				"Removed subscription filter for tenant %s",
				displayTenant,
			),
		},
	}, nil
}

func (a *subFilterRemoveAction) resolveTenant(
	ctx context.Context,
	subscriptions []account.Subscription,
) (string, string, error) {
	tenantNames, err := a.accountManager.GetTenantDisplayNames(ctx)
	if err != nil {
		tenantNames = nil
	}

	tenants := prompt.ExtractUniqueTenants(subscriptions, tenantNames)
	if len(tenants) == 0 {
		return "", "", fmt.Errorf("no tenants found")
	}

	if len(tenants) == 1 {
		return tenants[0].Id, tenants[0].DisplayName, nil
	}

	options := make([]string, len(tenants))
	for i, t := range tenants {
		options[i] = prompt.FormatTenantDisplay(i+1, t)
	}

	selectedIndex, err := a.console.Select(ctx, input.ConsoleOptions{
		Message: "Select a tenant to remove the subscription filter for",
		Options: options,
	})
	if err != nil {
		return "", "", fmt.Errorf("selecting tenant: %w", err)
	}

	return tenants[selectedIndex].Id,
		tenants[selectedIndex].DisplayName, nil
}

// subscriptionIdsByDisplayName maps selected display strings back
// to subscription IDs.
func subscriptionIdsByDisplayName(
	subscriptions []account.Subscription,
	displayFn func(*account.Subscription) string,
	selected []string,
) []string {
	optToId := make(map[string]string, len(subscriptions))
	for i := range subscriptions {
		optToId[displayFn(&subscriptions[i])] = subscriptions[i].Id
	}

	ids := make([]string, 0, len(selected))
	for _, opt := range selected {
		if id, ok := optToId[opt]; ok {
			ids = append(ids, id)
		}
	}

	slices.Sort(ids)
	return ids
}
