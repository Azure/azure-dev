// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bothGenerated is a finished run that produced a dataset and an evaluator.
func bothGenerated() []generationOutcome {
	return []generationOutcome{
		{
			plan: generationPlan{
				Kind: generateKindDataset, Agent: "hero-agent", EvaluationLevel: "turn",
			},
			ref:    &project.ArtifactRef{Name: "hero-agent-turn-tests"},
			report: generationReport{jobID: "datagen-1"},
		},
		{
			plan:   generationPlan{Kind: generateKindEvaluator, Agent: "hero-agent"},
			ref:    &project.ArtifactRef{Name: "hero-agent-evaluator"},
			report: generationReport{jobID: "evaluatorgen-1"},
		},
	}
}

// Generation used to end on its last download line. The job ids -- the only
// handle on a billed job once the command exits -- scrolled past unlabelled,
// and the caller was left to work out that `init` came next and to retype every
// name the command had just chosen for them.
func TestGenerationClosesWithItsJobsAndTheNextCommand(t *testing.T) {
	var out bytes.Buffer

	writeGenerationCompleted(&out, bothGenerated())

	text := out.String()
	assert.Contains(t, text, "Generation completed")
	assert.Contains(t, text, "dataset job: datagen-1")
	assert.Contains(t, text, "evaluator job: evaluatorgen-1")
	assert.Contains(t, text, "Next: azd ai eval init")
}

// The handoff runs exactly as printed, so every value it needs is in it.
func TestTheInitHandoffCarriesEveryValueItNeeds(t *testing.T) {
	got := initHandoff(bothGenerated())

	assert.Contains(t, got, "--target hero-agent",
		"init can detect this, but the printed line has to run as printed")
	assert.Contains(t, got, "--source dataset")
	assert.Contains(t, got, "--dataset hero-agent-turn-tests")
	assert.Contains(t, got, "--evaluation-level turn")
	assert.Contains(t, got, "--evaluator builtin.task_completion")
	assert.Contains(t, got, "--evaluator hero-agent-evaluator")
	assert.NotContains(t, got, "<", "a line with a placeholder in it is not a command")
}

// Only what was generated is named. Pointing at a dataset that was never
// produced prints a command that fails on its first flag.
func TestTheHandoffNamesOnlyWhatWasGenerated(t *testing.T) {
	dataset := bothGenerated()[:1]
	evaluator := bothGenerated()[1:]

	datasetOnly := initHandoff(dataset)
	assert.Contains(t, datasetOnly, "--dataset hero-agent-turn-tests")
	assert.NotContains(t, datasetOnly, "--evaluator")

	evaluatorOnly := initHandoff(evaluator)
	assert.Contains(t, evaluatorOnly, "--evaluator hero-agent-evaluator")
	assert.NotContains(t, evaluatorOnly, "--dataset")
	assert.NotContains(t, evaluatorOnly, "--source")
}

// Nothing produced is nothing to hand off. A --no-wait run has job ids and no
// artifacts, and `init` cannot be pointed at either of them yet.
func TestNothingProducedPrintsNoHandoff(t *testing.T) {
	outcomes := []generationOutcome{{
		plan:   generationPlan{Kind: generateKindDataset},
		report: generationReport{jobID: "datagen-1"},
	}}

	require.Empty(t, initHandoff(outcomes))

	var out bytes.Buffer
	writeGenerationCompleted(&out, outcomes)
	assert.Contains(t, out.String(), "datagen-1", "the job id is still worth having")
	assert.NotContains(t, out.String(), "Next:")
}
