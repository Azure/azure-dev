// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestDispatchActionFallsBackAfterCleaningUpEmptyProviders(t *testing.T) {
	t.Parallel()

	cleaned, legacyRan := false, false
	action := NewDispatchAction(nil, false, func(context.Context) ([]ProviderItem, func(), error) {
		return nil, func() { cleaned = true }, nil
	}, func() (actions.Action, error) {
		require.True(t, cleaned)
		return actions.ActionFunc(func(context.Context) (*actions.ActionResult, error) {
			legacyRan = true
			return nil, nil
		}), nil
	})
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.True(t, legacyRan)
}

func TestDispatchActionCleansUpAfterCanceledSelection(t *testing.T) {
	t.Parallel()

	promptErr := errors.New("selection canceled")
	cleaned := false
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) {
			require.False(t, cleaned)
			return -1, promptErr
		})
	action := NewDispatchAction(console, false, func(context.Context) ([]ProviderItem, func(), error) {
		return []ProviderItem{{DisplayName: "Example"}}, func() { cleaned = true }, nil
	}, nil)
	_, err := action.Run(t.Context())
	require.ErrorIs(t, err, promptErr)
	require.True(t, cleaned)
}
