// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestDispatchActionReturnsToMenuAfterUnavailableItem(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	menuCalls := 0
	acknowledged := false
	console.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(options input.ConsoleOptions) (any, error) {
			require.Equal(t, []string{"Event Hubs", "Service Bus"}, options.Options)
			menuCalls++
			if menuCalls == 1 {
				return 0, nil
			}
			require.True(t, acknowledged)
			return 1, nil
		})
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return options.Message == "Press Enter to return to the add menu"
	}).RespondFn(func(input.ConsoleOptions) (any, error) {
		require.Contains(t, strings.Join(console.Output(), "\n"), "this layer already manages an Event Hubs namespace")
		acknowledged = true
		return "", nil
	})
	added := false
	action := NewDispatchAction(console, false, func(context.Context) ([]ProviderItem, func(), error) {
		return []ProviderItem{
			{DisplayName: "Event Hubs", Run: func(context.Context) error {
				return azdext.UnwrapError(azdext.WrapError(&azdext.LocalError{
					Category: azdext.LocalErrorCategoryValidation, Code: azdext.AddItemUnavailableCode,
					Message: "this layer already manages an Event Hubs namespace",
				}))
			}},
			{DisplayName: "Service Bus", Run: func(context.Context) error {
				added = true
				return nil
			}},
		}, nil, nil
	}, nil)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, menuCalls)
	require.True(t, added)
}

func TestDispatchActionReturnsOtherProviderErrors(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	console.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(0)
	runErr := errors.New("saving project failed")
	action := NewDispatchAction(console, false, func(context.Context) ([]ProviderItem, func(), error) {
		return []ProviderItem{{DisplayName: "Example", Run: func(context.Context) error { return runErr }}}, nil, nil
	}, nil)

	_, err := action.Run(t.Context())
	require.ErrorIs(t, err, runErr)
	require.Equal(t, []string{"What would you like to add?"}, console.Output())
}

func TestDispatchActionFallsBackAfterCleaningUpEmptyProviders(t *testing.T) {
	t.Parallel()

	cleaned := false
	fallbackErr := errors.New("no providers contributed items")
	action := NewDispatchAction(nil, false, func(context.Context) ([]ProviderItem, func(), error) {
		return nil, func() { cleaned = true }, nil
	}, func(context.Context) error {
		require.True(t, cleaned)
		return fallbackErr
	})
	_, err := action.Run(t.Context())
	require.ErrorIs(t, err, fallbackErr)
}

func TestDispatchActionCleansUpAfterCanceledSelection(t *testing.T) {
	t.Parallel()

	promptErr := errors.New("selection canceled")
	cleaned := false
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(options input.ConsoleOptions) (any, error) {
			require.False(t, cleaned)
			require.True(t, options.FitToTerminal)
			return -1, promptErr
		})
	action := NewDispatchAction(console, false, func(context.Context) ([]ProviderItem, func(), error) {
		return []ProviderItem{{DisplayName: "Example"}}, func() { cleaned = true }, nil
	}, nil)
	_, err := action.Run(t.Context())
	require.ErrorIs(t, err, promptErr)
	require.True(t, cleaned)
}
