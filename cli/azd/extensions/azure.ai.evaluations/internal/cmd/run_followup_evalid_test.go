// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
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

	// A declared name can resolve to a newer eval after redeploy, so commands
	// must retain the immutable eval ID even when a friendly label is known.
	declared := &eval_api.OpenAIEvalRun{
		ID:       "evalrun_1",
		EvalID:   "evalgroup_7",
		Metadata: map[string]string{metaEvalName: "nightly"},
	}
	assert.Equal(t, "evalgroup_7", followUpEvalRef(declared))
	declared.EvalID = ""
	assert.Equal(t, "nightly", followUpEvalRef(declared), "use the name only when no immutable ID is available")

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

func TestRunFollowUpCommandsKeepImmutableIdentityAndFriendlyLabelsSeparate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		render func(io.Writer, *eval_api.OpenAIEvalRun) error
	}{
		{"summary", func(out io.Writer, run *eval_api.OpenAIEvalRun) error {
			return renderRun(out, runForDisplay(run, "eval_immutable", run.ID), nil)
		}},
		{"detail", func(out io.Writer, run *eval_api.OpenAIEvalRun) error {
			return renderRunDetail(out, runForDisplay(run, "eval_immutable", run.ID))
		}},
		{"output", func(out io.Writer, run *eval_api.OpenAIEvalRun) error {
			return renderResults(out, "eval_immutable", run, []eval_api.OutputItem{failingItem("1")}, false)
		}},
	} {
		for _, idSource := range []string{"service", "resolved lookup"} {
			t.Run(tc.name+"/"+idSource, func(t *testing.T) {
				run := simulationReportingRun()
				run.EvalID = ""
				if idSource == "service" {
					run.EvalID = "eval_immutable"
				}
				var firstCommands []string
				for _, label := range []string{"nightly-old-label", "nightly-new-label"} {
					run.Metadata[metaEvalName] = label
					before, err := json.Marshal(run)
					require.NoError(t, err)
					var out bytes.Buffer
					require.NoError(t, tc.render(&out, run))
					assert.Contains(t, out.String(), "Eval       "+label,
						"the familiar name belongs in the label, not the command argument")
					var commands []string
					for line := range strings.SplitSeq(out.String(), "\n") {
						if !strings.Contains(line, "azd ai eval run output") {
							continue
						}
						assert.Contains(t, line, "--eval eval_immutable")
						assert.Contains(t, line, "--run "+run.ID)
						assert.NotContains(t, line, label)
						commands = append(commands, line)
					}
					require.GreaterOrEqual(t, len(commands), 2)
					if firstCommands == nil {
						firstCommands = commands
					} else {
						assert.Equal(t, firstCommands, commands, "changed labels must not retarget prior run commands")
					}
					after, err := json.Marshal(run)
					require.NoError(t, err)
					assert.JSONEq(t, string(before), string(after),
						"human rendering does not alter raw run identity or metadata")
				}
			})
		}
	}
}
