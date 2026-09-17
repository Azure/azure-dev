// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

// The CI path: start a run without waiting, read the handoff, come back for
// the result later. Everything here is what a pipeline does, so it is driven
// through the binary exactly the way a pipeline would.

package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLIStartNoWaitEmitsTheHandoff pins the JSON a pipeline reads.
//
// A script captures the run id here and reattaches to it in a later step, so
// the field names are a contract. Emitting the service's run object instead
// would make that script depend on a shape this extension does not control.
func TestCLIStartNoWaitEmitsTheHandoff(t *testing.T) {
	f := sharedEval(t)

	r := requireSuccess(t, run(t,
		"run", "start", "--eval", f.EvalID, "--no-wait", "-o", "json"))

	var handoff struct {
		RunID     string `json:"run_id"`
		EvalID    string `json:"eval_id"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}
	r.JSON(t, &handoff)

	require.NotEmpty(t, handoff.RunID, "a pipeline has nothing to reattach to without run_id")
	assert.Equal(t, f.EvalID, handoff.EvalID)
	assert.NotEmpty(t, handoff.Status)

	// Started, not finished: this is the whole point of --no-wait, and a
	// command that quietly blocked would pass every other assertion here.
	assert.NotEqual(t, "completed", handoff.Status)

	deferTeardown(func() {
		runQuietly("run", "cancel", handoff.RunID, "--eval", f.EvalID)
	})

	// The id it handed back has to be one the next step can use.
	shown := requireSuccess(t, run(t,
		"run", "show", handoff.RunID, "--eval", f.EvalID, "-o", "json"))
	var reattached struct {
		ID string `json:"id"`
	}
	shown.JSON(t, &reattached)
	assert.Equal(t, handoff.RunID, reattached.ID,
		"the run id in the handoff must be the one `run show` resolves")
}

// TestCLIStartNoWaitTellsAPersonHowToReattach covers the same path without
// -o json, where what matters is that the printed command is one that works
// rather than a sentence containing a placeholder.
func TestCLIStartNoWaitTellsAPersonHowToReattach(t *testing.T) {
	f := sharedEval(t)

	r := requireSuccess(t, run(t, "run", "start", "--eval", f.EvalID, "--no-wait"))

	assert.Contains(t, r.Stdout, "Reattach with: azd ai eval run show")
	assert.Contains(t, r.Stdout, f.EvalID,
		"the reattach line must carry the eval id, not a placeholder for it")
	assert.NotContains(t, r.Stdout, "<",
		"nothing printed for a person to copy may contain a placeholder")

	var runID string
	for _, field := range strings.Fields(r.Stdout) {
		if strings.HasPrefix(field, "evalrun_") {
			runID = field
			break
		}
	}
	require.NotEmpty(t, runID, "the run id must be printed:\n%s", r.Stdout)
	deferTeardown(func() { runQuietly("run", "cancel", runID, "--eval", f.EvalID) })
}
