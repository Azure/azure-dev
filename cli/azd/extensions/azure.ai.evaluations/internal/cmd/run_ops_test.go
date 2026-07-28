// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestSummarizeCounts(t *testing.T) {
	require.Equal(t, "", summarizeCounts(nil))
	require.Equal(t, "3 passed, 1 failed, 0 errored",
		summarizeCounts(&eval_api.EvalRunResultCounts{Total: 4, Passed: 3, Failed: 1}))
}

// Cancelling a finished run is rejected locally. The service reports success
// either way, so without this the CLI would claim it cancelled a run that had
// already completed.
func TestTerminalRunStatesCoverServiceVocabulary(t *testing.T) {
	for _, status := range []string{"completed", "failed", "canceled", "cancelled", "error"} {
		require.True(t, terminalRunStates[status], "%q should be terminal", status)
	}
	for _, status := range []string{"in_progress", "queued", "running", ""} {
		require.False(t, terminalRunStates[status], "%q should not be terminal", status)
	}
}

// The atomic run operations have to be reachable as subcommands; the spec
// requires start, list, show and cancel to exist alongside the composite.
func TestRunCommandExposesAtomicSubcommands(t *testing.T) {
	cmd := newRunCommand()

	found := map[string]bool{}
	for _, sub := range cmd.Commands() {
		found[sub.Name()] = true
	}
	for _, name := range []string{"start", "list", "show", "cancel"} {
		require.True(t, found[name], "run should expose the %q subcommand", name)
	}
}

// `run start` is the atomic form of the composite and must accept the same
// flags, otherwise the two forms diverge.
func TestRunStartMirrorsCompositeFlags(t *testing.T) {
	composite := newRunCommand()

	var start *cobra.Command
	for _, sub := range composite.Commands() {
		if sub.Name() == "start" {
			start = sub
		}
	}
	require.NotNil(t, start)

	for _, flag := range []string{"eval-id", "eval-group", "name", "level", "max-samples", "wait", "no-wait"} {
		require.NotNil(t, start.Flags().Lookup(flag), "run start should accept --%s", flag)
	}
}

// --no-wait is documented in the spec, and cobra does not derive it from the
// --wait bool.
func TestRunCommandAcceptsNoWait(t *testing.T) {
	cmd := newRunCommand()
	require.NotNil(t, cmd.Flags().Lookup("no-wait"), "run should accept --no-wait")
	require.NotNil(t, cmd.Flags().Lookup("wait"), "run should keep --wait")
}
