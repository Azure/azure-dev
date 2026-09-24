// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A run created by the portal or an SDK carries none of this extension's
// metadata, so the follow-up printed `--run <id>` with no `--eval`. That
// command has to re-resolve the eval from the configuration, which prompts or
// picks a declaration the run does not belong to -- a listing aimed at the
// wrong eval. The service states the id on the run, so it stands in.
func TestFollowUpNamesTheEvalForARunThisExtensionDidNotCreate(t *testing.T) {
	t.Parallel()

	external := &eval_api.OpenAIEvalRun{ID: "evalrun_1", EvalID: "evalgroup_7"}
	assert.Equal(t, "evalgroup_7", followUpEvalRef(external),
		"the run states its own eval even with no metadata")

	// The declared name still wins, because that is what a reader has in
	// their configuration.
	declared := &eval_api.OpenAIEvalRun{
		ID:       "evalrun_1",
		EvalID:   "evalgroup_7",
		Metadata: map[string]string{metaEvalName: "nightly"},
	}
	assert.Equal(t, "nightly", followUpEvalRef(declared))

	// Nothing to name stays empty rather than inventing a reference.
	assert.Empty(t, followUpEvalRef(&eval_api.OpenAIEvalRun{ID: "evalrun_1"}))
}

// The point of the fallback is the command text, so that is what is asserted:
// an external run's suggestion has to be runnable as printed.
func TestTheSuggestedCommandsCarryTheEvalForAnExternalRun(t *testing.T) {
	t.Parallel()

	external := &eval_api.OpenAIEvalRun{ID: "evalrun_1", EvalID: "evalgroup_7"}

	followUp := messages.RunFollowUp(followUpEvalRef(external), external.ID, true, true)

	assert.Contains(t, followUp, "--eval evalgroup_7",
		"the printed command has to be directly executable")
	assert.Contains(t, followUp, "--run evalrun_1")
}

// Through the renderer, not the helper. The defect was a wrong *argument* at
// the call site, and a test that supplies the argument itself cannot see that
// -- reverting `followUpEvalRef(run)` to `run.Metadata[metaEvalName]` leaves
// the two tests above green.
func TestTheRenderedFollowUpNamesTheEvalForAnExternalRun(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	external := &eval_api.OpenAIEvalRun{
		ID:     "evalrun_1",
		EvalID: "evalgroup_7",
		Status: "completed",
		ResultCounts: &eval_api.EvalRunResultCounts{
			Total: 4, Passed: 1, Failed: 2, Errored: 1,
		},
	}

	require.NoError(t, renderRun(&out, external, nil))

	rendered := out.String()
	assert.Contains(t, rendered, "--eval evalgroup_7",
		"the follow-up a reader copies has to name the eval the run belongs to")

	// The eval-less form is what the defect printed. Matching the command
	// prefix rather than a fragment, so a correct line cannot satisfy it by
	// containing the same words further along.
	assert.NotContains(t, rendered, "run output list --run ",
		"a command with no --eval would have to re-resolve the eval")
	assert.NotContains(t, rendered, "run output export --run ")
}
