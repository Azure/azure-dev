// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestInteractiveGuard_RejectsNoPrompt(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	console.SetNoPromptMode(true)
	guard := NewInteractiveGuardMiddleware(console, &Options{CommandPath: "azd pipeline config"})

	nextCalled := false
	result, err := guard.Run(t.Context(), func(context.Context) (*actions.ActionResult, error) {
		nextCalled = true
		return nil, nil
	})

	require.Nil(t, result)
	require.False(t, nextCalled, "later middleware and the action must not run in no-prompt mode")
	require.ErrorIs(t, err, internal.ErrInteractiveRequired)
	require.EqualError(t, err,
		"azd pipeline config requires interactive input and cannot be run with --no-prompt: "+
			"interactive mode required")

	suggestionErr, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
	require.True(t, ok)
	require.Contains(t, suggestionErr.Message, "No changes were made")
	require.Empty(t, suggestionErr.Suggestion)
}

func TestInteractiveGuard_AllowsInteractive(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	guard := NewInteractiveGuardMiddleware(console, &Options{CommandPath: "azd pipeline config"})

	expected := &actions.ActionResult{}
	result, err := guard.Run(t.Context(), func(context.Context) (*actions.ActionResult, error) {
		return expected, nil
	})

	require.NoError(t, err)
	require.Same(t, expected, result)
}
