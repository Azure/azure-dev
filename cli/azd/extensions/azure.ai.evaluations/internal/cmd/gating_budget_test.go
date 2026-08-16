// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"strings"
	"testing"
	"time"

	"azureaieval/internal/messages"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A run that outlives the wait leaves --fail-on with nothing to judge. Exiting
// 0 there tells a pipeline the gate passed, which is the silent drop --no-wait
// is refused for, reached by running long instead. The message has to say the
// gate did not run, not merely that the wait stopped.
func TestGateOutlivedTheWaitSaysTheGateNeverRan(t *testing.T) {
	err := messages.GateOutlivedTheWait("run_abc", 2*time.Hour)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "run_abc")
	assert.Contains(t, err.Error(), "2h0m0s", "the message has to say how long it waited")
	assert.Contains(t, err.Error(), "--fail-on",
		"a reader has to know which flag went unanswered")
	assert.Contains(t, err.Error(), "run show",
		"and how to get the verdict they asked for")
}

// The check above only proves the message is right, not that anything calls it.
// Driving the branch itself needs a run that outlives a two-hour const budget
// and a signed-in client, so this reads the source instead.
//
// Worth the ugliness here: the failure mode is a gate that passes silently, so
// a regression looks exactly like success and no other test would notice. Same
// reasoning as the linker-path check in internal/version.
func TestWaitBudgetBranchStillConsultsTheGate(t *testing.T) {
	body, err := os.ReadFile("run.go")
	require.NoError(t, err)

	src := string(body)
	start := strings.Index(src, "errors.Is(err, errWaitBudgetSpent)")
	require.NotEqual(t, -1, start, "the wait-budget branch has moved or gone")

	branch := src[start:]
	if end := strings.Index(branch, "\n\t\t\tif err != nil {"); end != -1 {
		branch = branch[:end]
	}

	assert.Contains(t, branch, "threshold.set",
		"the branch has to ask whether a gate was set before reporting success")
	assert.Contains(t, branch, "GateOutlivedTheWait",
		"and refuse rather than exit 0 when one was")
}
