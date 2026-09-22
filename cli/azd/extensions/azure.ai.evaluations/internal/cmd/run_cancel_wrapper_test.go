// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"testing"

	"azureaieval/internal/messages"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answering hands evalIDForRunCommand a resolution without a project behind it.
func answering(t *testing.T, id string, err error) *cobra.Command {
	t.Helper()
	restore := resolveEvalIDFn
	resolveEvalIDFn = func(*cobra.Command, *evalContext, string) (string, error) {
		return id, err
	}
	t.Cleanup(func() { resolveEvalIDFn = restore })

	cmd := &cobra.Command{Use: "list"}
	cmd.SetOut(&bytes.Buffer{})
	return cmd
}

// out reads back what a command wrote.
func out(cmd *cobra.Command) string {
	return cmd.OutOrStdout().(*bytes.Buffer).String()
}

// Closing the picker is an answer: nothing failed, and there is nothing left to
// list, show, cancel or export. The run subcommands used to return the sentinel
// as a command error, so one keystroke printed "Cancelled." at `eval create`
// and exited non-zero at the other six.
func TestEvalIDForRunCommand_ACancelledPickerIsNotAFailure(t *testing.T) {
	cmd := answering(t, "", errEvalSelectionCancelled)

	evalID, carryOn, err := evalIDForRunCommand(cmd, nil, "")

	require.NoError(t, err, "a deliberate answer must not exit non-zero")
	assert.False(t, carryOn, "there is no eval to act on")
	assert.Empty(t, evalID)
	assert.Equal(t, messages.EvalSelectionCancelled(), out(cmd),
		"and the reader is told, in the same words create uses")
}

// The sentinel is recognized through the error chain, not by identity: the
// picker's error travels back up through the resolution that called it.
func TestEvalIDForRunCommand_RecognizesAWrappedSentinel(t *testing.T) {
	wrapped := errors.Join(errors.New("reading the configuration"), errEvalSelectionCancelled)
	cmd := answering(t, "", wrapped)

	_, carryOn, err := evalIDForRunCommand(cmd, nil, "")

	require.NoError(t, err)
	assert.False(t, carryOn)
}

// Everything else is still a failure. Reporting a closed picker as an answer
// must not turn a project that cannot be read into a silent exit 0.
func TestEvalIDForRunCommand_LeavesEveryOtherErrorAlone(t *testing.T) {
	failure := errors.New("no azd project")
	cmd := answering(t, "", failure)

	_, carryOn, err := evalIDForRunCommand(cmd, nil, "")

	require.ErrorIs(t, err, failure)
	assert.False(t, carryOn)
	assert.Empty(t, out(cmd), "a failure is not a cancellation, and must not read as one")
}

// A resolved eval carries on, so the wrapper is not swallowing the normal path.
func TestEvalIDForRunCommand_APickedEvalCarriesOn(t *testing.T) {
	cmd := answering(t, "eval-123", nil)

	evalID, carryOn, err := evalIDForRunCommand(cmd, nil, "")

	require.NoError(t, err)
	assert.True(t, carryOn)
	assert.Equal(t, "eval-123", evalID)
	assert.Empty(t, out(cmd))
}
