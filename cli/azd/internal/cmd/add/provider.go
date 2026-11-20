// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// ProviderItem represents one selectable action from an installed add provider.
type ProviderItem struct {
	ID          string
	DisplayName string
	Provider    string
	Run         func(context.Context) error
}

// NewDispatchAction constructs the add action without resolving legacy project or Azure dependencies.
func NewDispatchAction(
	console input.Console,
	noPrompt bool,
	discover func(context.Context) ([]ProviderItem, func(), error),
	resolveLegacy func() (actions.Action, error),
) actions.Action {
	return &dispatchAction{
		console:       console,
		noPrompt:      noPrompt,
		discover:      discover,
		resolveLegacy: resolveLegacy,
	}
}

type dispatchAction struct {
	console       input.Console
	noPrompt      bool
	discover      func(context.Context) ([]ProviderItem, func(), error)
	resolveLegacy func() (actions.Action, error)
}

func (a *dispatchAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	handled, err := a.runProvider(ctx)
	if handled || err != nil {
		return nil, err
	}

	legacy, err := a.resolveLegacy()
	if err != nil {
		return nil, err
	}
	return legacy.Run(ctx)
}

func (a *dispatchAction) runProvider(ctx context.Context) (bool, error) {
	items, cleanup, err := a.discover(ctx)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return false, err
	}
	if len(items) == 0 {
		return false, nil
	}
	if a.noPrompt {
		return true, errors.New("selecting a component to add requires interactive input. Run azd add without --no-prompt")
	}

	items = slices.Clone(items)
	slices.SortFunc(items, func(a, b ProviderItem) int {
		return cmp.Or(cmp.Compare(a.DisplayName, b.DisplayName), cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.ID, b.ID))
	})
	idx, err := a.console.Select(ctx, input.ConsoleOptions{
		Message: "What would you like to add?",
		Options: providerLabels(items),
	})
	if err != nil {
		return true, err
	}
	return true, items[idx].Run(ctx)
}

func providerLabels(items []ProviderItem) []string {
	counts := make(map[string]int, len(items))
	for _, item := range items {
		counts[item.DisplayName]++
	}
	used := make(map[string]bool, len(items))
	for label, count := range counts {
		used[label] = count == 1
	}

	labels := make([]string, len(items))
	for i, item := range items {
		label := item.DisplayName
		if counts[label] > 1 {
			// Console selection matches labels, not just indices. Reserve unique original
			// labels so a generated suffix cannot hide another provider's item.
			base := fmt.Sprintf("%s (%s: %s)", label, item.Provider, item.ID)
			label = base
			for j := 2; used[label]; j++ {
				label = fmt.Sprintf("%s [%d]", base, j)
			}
			used[label] = true
		}
		labels[i] = label
	}
	return labels
}
