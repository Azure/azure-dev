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
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// ProviderItem represents one selectable action from an installed add provider.
type ProviderItem struct {
	ID             string
	DisplayName    string
	Provider       string
	Category       string
	DisabledReason string
	Run            func(context.Context) error
}

// NewDispatchAction constructs the add action. onEmpty reports what to do when no provider contributes items.
func NewDispatchAction(
	console input.Console,
	noPrompt bool,
	discover func(context.Context) ([]ProviderItem, func(), error),
	onEmpty func(context.Context) error,
) actions.Action {
	return &dispatchAction{
		console:  console,
		noPrompt: noPrompt,
		discover: discover,
		onEmpty:  onEmpty,
	}
}

type dispatchAction struct {
	console  input.Console
	noPrompt bool
	discover func(context.Context) ([]ProviderItem, func(), error)
	onEmpty  func(context.Context) error
}

func (a *dispatchAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	handled, err := a.runProvider(ctx)
	if handled || err != nil {
		return nil, err
	}

	return nil, a.onEmpty(ctx)
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
		return cmp.Or(
			cmp.Compare(a.Category, b.Category),
			cmp.Compare(a.DisplayName, b.DisplayName),
			cmp.Compare(a.Provider, b.Provider),
			cmp.Compare(a.ID, b.ID),
		)
	})
	options := input.ConsoleOptions{
		Message:       "What would you like to add?",
		Options:       providerLabels(items),
		FitToTerminal: true,
	}
	options.DisabledReasons = make([]string, len(items))
	for i, item := range items {
		options.DisabledReasons[i] = item.DisabledReason
	}
	for {
		idx, err := a.console.Select(ctx, options)
		if err != nil {
			return true, err
		}

		if reason := items[idx].DisabledReason; reason != "" {
			return true, fmt.Errorf("%s is unavailable: %s", items[idx].DisplayName, reason)
		}
		err = items[idx].Run(ctx)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		if !ok || localErr.Category != azdext.LocalErrorCategoryValidation || localErr.Code != azdext.AddItemUnavailableCode {
			return true, err
		}

		a.console.MessageUxItem(ctx, &ux.ErrorWithSuggestion{
			Message: localErr.Message, Suggestion: localErr.Suggestion, Links: localErr.Links,
		})
		if _, err := a.console.Prompt(ctx, input.ConsoleOptions{Message: "Press Enter to return to the add menu"}); err != nil {
			return true, err
		}
	}
}

func providerLabels(items []ProviderItem) []string {
	counts := make(map[string]int, len(items))
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = fmt.Sprintf("(%s) %s", item.Category, item.DisplayName)
		counts[labels[i]]++
	}
	used := make(map[string]bool, len(items))
	for label, count := range counts {
		used[label] = count == 1
	}

	for i, item := range items {
		label := labels[i]
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
