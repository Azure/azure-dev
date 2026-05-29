// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
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
	if a.console.IsNoPromptMode() {
		return nil, fmt.Errorf(
			"subscription filter set requires interactive mode (cannot run with --no-prompt): %w",
			internal.ErrInteractiveRequired,
		)
	}

	// Load subscriptions
	var subscriptions []account.Subscription
	loadingSpinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:   "Loading subscriptions...",
		Writer: spinnerWriter(a.console),
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
		return nil, fmt.Errorf(
			"no subscriptions found: %w",
			internal.ErrNoSubscriptionsFound,
		)
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
			"no subscriptions found for tenant %s: %w",
			tenantId, internal.ErrNoSubscriptionsFound,
		)
	}

	// Sort subscriptions by name for a stable, scannable list
	slices.SortFunc(tenantSubs, func(a, b account.Subscription) int {
		return cmp.Compare(
			strings.ToLower(a.Name), strings.ToLower(b.Name),
		)
	})

	// Build options for multi-select with unique index prefixes
	// to avoid ambiguity when display names collide (e.g. demo mode).
	hideId := prompt.IsDemoModeEnabled()

	options := make([]string, len(tenantSubs))
	for i := range tenantSubs {
		label := prompt.FormatSubscriptionDisplay(
			&tenantSubs[i], hideId,
		)
		options[i] = fmt.Sprintf("%d. %s", i+1, label)
	}

	// displayFn must match the indexed option format for preSelected matching
	indexedDisplayFn := func(
		sub *account.Subscription, idx int,
	) string {
		label := prompt.FormatSubscriptionDisplay(sub, hideId)
		return fmt.Sprintf("%d. %s", idx+1, label)
	}

	// Load existing filter to pre-check items
	cfg, err := a.userConfigManager.Load()
	if err != nil {
		return nil, fmt.Errorf("loading user config: %w", err)
	}

	existingFilter, _ := prompt.LoadSubscriptionFilter(cfg, tenantId)

	// Build preSelected with indexed labels
	var preSelected []string
	if len(existingFilter) > 0 {
		filterSet := make(map[string]bool, len(existingFilter))
		for _, id := range existingFilter {
			filterSet[strings.ToLower(id)] = true
		}
		for i := range tenantSubs {
			if filterSet[strings.ToLower(tenantSubs[i].Id)] {
				preSelected = append(
					preSelected,
					indexedDisplayFn(&tenantSubs[i], i),
				)
			}
		}
	}

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

	// Map selected indexed options back to subscription IDs.
	// Each option string is unique due to the index prefix.
	optionToId := make(map[string]string, len(tenantSubs))
	for i := range tenantSubs {
		optionToId[options[i]] = tenantSubs[i].Id
	}

	selectedIds := make([]string, 0, len(selected))
	for _, opt := range selected {
		if id, ok := optionToId[opt]; ok {
			selectedIds = append(selectedIds, id)
		}
	}
	slices.Sort(selectedIds)

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

	subWord := "subscription"
	if len(selectedIds) != 1 {
		subWord = "subscriptions"
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf(
				"Saved subscription filter for tenant %s"+
					" (%d %s)",
				displayTenant,
				len(selectedIds),
				subWord,
			),
		},
	}, nil
}

func (a *subFilterSetAction) resolveTenant(
	ctx context.Context,
	subscriptions []account.Subscription,
) (string, string, error) {
	return resolveTenantForFilter(
		ctx, subscriptions, a.console, a.accountManager,
		"Select a tenant to set a subscription filter for",
	)
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
	if a.console.IsNoPromptMode() {
		return nil, fmt.Errorf(
			"subscription filter remove requires interactive mode (cannot run with --no-prompt): %w",
			internal.ErrInteractiveRequired,
		)
	}

	// Load subscriptions for tenant resolution
	var subscriptions []account.Subscription
	loadingSpinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:   "Loading subscriptions...",
		Writer: spinnerWriter(a.console),
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
		return nil, fmt.Errorf(
			"no subscriptions found: %w",
			internal.ErrNoSubscriptionsFound,
		)
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
	return resolveTenantForFilter(
		ctx, subscriptions, a.console, a.accountManager,
		"Select a tenant to remove the subscription filter for",
	)
}

// resolveTenantForFilter resolves the tenant to use for a sub-filter
// operation. If only one tenant exists, it is returned directly without
// an API call for display names. Otherwise the user is prompted.
func resolveTenantForFilter(
	ctx context.Context,
	subscriptions []account.Subscription,
	console input.Console,
	accountManager account.Manager,
	promptMessage string,
) (string, string, error) {
	// Extract tenants without display names first to avoid an unnecessary
	// API call when only a single tenant exists.
	tenants := prompt.ExtractUniqueTenants(subscriptions, nil)
	if len(tenants) == 0 {
		return "", "", fmt.Errorf(
			"no tenants found: %w", internal.ErrNoTenantsFound,
		)
	}

	if len(tenants) == 1 {
		return tenants[0].Id, tenants[0].DisplayName, nil
	}

	// Multiple tenants — fetch display names for the prompt
	tenantNames, err := accountManager.GetTenantDisplayNames(ctx)
	if err != nil {
		tenantNames = nil
	}

	tenants = prompt.ExtractUniqueTenants(subscriptions, tenantNames)

	options := make([]string, len(tenants))
	for i, t := range tenants {
		options[i] = prompt.FormatTenantDisplay(i+1, t)
	}

	selectedIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message: promptMessage,
		Options: options,
	})
	if err != nil {
		return "", "", fmt.Errorf("selecting tenant: %w", err)
	}

	return tenants[selectedIndex].Id,
		tenants[selectedIndex].DisplayName, nil
}

// spinnerWriter returns the console's writer for spinner output,
// falling back to io.Discard when nil (e.g. in tests).
func spinnerWriter(console input.Console) io.Writer {
	if w := console.GetWriter(); w != nil {
		return w
	}
	return io.Discard
}
