// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PROMPTS spec 6: the confirmation applies to `azd ai eval job delete`, and
// section 14 calls a path that bypasses one release-blocking.
//
// It was the only delete verb in the extension that removed the record without
// asking, and a job id is the easiest to mistype: the dataset and evaluator
// groups share an id shape.
func TestEveryDeleteVerbConfirmsAndTakesForce(t *testing.T) {
	root := NewRootCommand()
	for _, path := range [][]string{
		{"delete"},
		{"run", "delete"},
		{"dataset", "delete"},
		{"evaluator", "delete"},
		{"job", "delete"},
	} {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			cmd, _, err := root.Find(path)
			require.NoError(t, err)
			require.Equal(t, "delete", strings.Fields(cmd.Use)[0])
			assert.NotNil(t, cmd.Flags().Lookup("force"),
				"a delete that cannot be skipped deliberately is one that always asks, "+
					"which no script can use")
		})
	}
}

// The subject says what survives the delete, because the thing a reader is
// most likely to fear here is losing the artifact the job produced.
func TestJobDeleteSubjectSaysWhatSurvives(t *testing.T) {
	subject := messages.JobDeleteSubject("dataset", "datagen-1")

	assert.Contains(t, subject, "datagen-1")
	assert.Contains(t, subject, "not deleted")
}

// INIT spec 12 V-12: "Explicit --max-traces | Rejects zero and negative values."
// Zero is not a smaller window; it is an eval with nothing to read.
func TestInitRejectsAZeroTraceBudget(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			err := runEval(t, "init", "--no-prompt", "--source", "traces",
				"--target", "support-agent", "--judge-model", "m",
				"--max-traces", value, "--path", t.TempDir())

			require.Error(t, err)
			assert.Contains(t, err.Error(), "--max-traces")
		})
	}
}

// INIT spec 12 V-03: "Repeated evaluator references | Deduplicates them."
//
// The same reference twice makes the service grade every row twice against one
// evaluator and report it as two results.
func TestRepeatedEvaluatorsAreWrittenOnce(t *testing.T) {
	ref := evalcore.BuiltinPrefix + "coherence"
	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName:   "smoke",
		target:     "support-agent",
		dataset:    "prod-golden",
		evaluators: []string{ref, ref, evalcore.BuiltinPrefix + "groundedness"},
		judgeModel: "m",
	})

	assert.Equal(t,
		[]string{ref, evalcore.BuiltinPrefix + "groundedness"},
		plan.evaluatorNames())
}

// PROMPTS spec 4: an eval with no runs prints the command that makes one.
// It is the ordinary state of one that was just scaffolded, so the useful
// thing to say is what to do about it.
func TestTheNoRunsEmptyStateNamesTheCommandThatMakesOne(t *testing.T) {
	line := messages.EvalHasNoRunsLine("support-turn-eval")

	assert.Contains(t, line, "No runs found")
	assert.Contains(t, line, "azd ai eval run start --eval support-turn-eval",
		"the printed command has to run as printed")
}

// INIT spec 11: "Cancelled. No files were changed."
func TestCancelSaysNoFilesWereChanged(t *testing.T) {
	assert.Contains(t, messages.ScaffoldCancelled(), "No files were changed.")
}

// A guard for the guard: init's four are what it offers, not the service's
// catalogue, so a reference outside them is still reachable by flag. The spec
// asks for the four to be enforced; enforcing them would refuse
// `builtin.relevance`, which the service does have.
func TestEvaluatorsOutsideTheOfferedFourAreStillAccepted(t *testing.T) {
	require.NoError(t, validateEvaluatorRefs([]string{
		evalcore.BuiltinPrefix + "relevance",
		evalcore.BuiltinPrefix + "task_adherence",
	}))
	assert.NotContains(t, builtinEvaluators, evalcore.BuiltinPrefix+"relevance",
		"which is exactly the case an allow-list of the four would have broken")
}

var _ = project.EvalConfig{}
