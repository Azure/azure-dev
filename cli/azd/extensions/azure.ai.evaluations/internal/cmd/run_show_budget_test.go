// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pollRun answers a spent budget with a nil run, so the branch that handles it
// has to keep the run it already read rather than the one poll returned. The
// first version of this branch assigned over it and dereferenced nil, which
// panics with a stack trace on a run that simply took a long time.
//
// Read from the source because reaching the branch needs a run that outlives
// two hours; the shape is what the test can pin, and the shape is what was
// wrong. gating_budget_test.go pins the same thing for `run start`.
func TestRunShowKeepsTheRunThePollDidNotReturn(t *testing.T) {
	body, err := os.ReadFile("run_ops.go")
	require.NoError(t, err)
	source := string(body)

	require.Contains(t, source, "errWaitBudgetSpent",
		"run show has to answer the budget rather than surface the sentinel")
	require.Contains(t, source, "if pollErr != nil",
		"the window below ends here; renaming it must fail rather than panic")

	branch := source[strings.Index(source, "errWaitBudgetSpent"):]
	// Ended at the poll's own error check, so the window holds the budget branch
	// and nothing after it. Ending it later swept in the command's ordinary
	// `if isJSON(a.cmd)` block and the assertion below passed with the fix removed.
	branch = branch[:strings.Index(branch, "if pollErr != nil")]

	assert.NotContains(t, source, "run, err = ec.pollRun(",
		"assigning poll's result over the run leaves nil to dereference")
	assert.Contains(t, source, "final, pollErr := ec.pollRun(",
		"poll's result belongs in its own variable")
	assert.Contains(t, branch, "isJSON(a.cmd)",
		"a human sentence in the JSON stream breaks every parser downstream")
}

// The gate is the one place a pipeline is guaranteed to read, so a budget that
// ran out under --fail-on has to be a refusal rather than a silent pass.
func TestRunShowRefusesAGateThatOutlivedTheWait(t *testing.T) {
	body, err := os.ReadFile("run_ops.go")
	require.NoError(t, err)

	assert.Contains(t, string(body), "GateOutlivedTheWait",
		"exiting 0 here would tell a pipeline the gate passed")
}
