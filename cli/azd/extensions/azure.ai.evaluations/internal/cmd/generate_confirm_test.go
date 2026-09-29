// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The plan is what the reader is being asked to pay for, so everything the
// generation will spend on has to be in it.
//
// Generation calls a model and registers artifacts in a shared project, and
// none of that was confirmed: the first thing a reader saw was a job id for
// work already submitted.
func TestTheGenerationPlanStatesWhatWillBeBilled(t *testing.T) {
	var out bytes.Buffer
	writeGenerationPlan(&out, generationSummary{
		model:       "gpt-4o-mini",
		instructed:  "deployed agent",
		projectName: "contoso-project",
		configPath:  "evals/azure.eval.yaml",
		plans: []generationPlan{
			{
				Kind: generateKindDataset, Name: "support-agent-turn-tests",
				Agent: "support-agent", SampleSize: 15, From: []string{"agent"},
				EvaluationLevel: "turn",
			},
			{Kind: generateKindEvaluator, Name: "support-agent-evaluator", Agent: "support-agent"},
		},
	})

	text := out.String()
	for _, want := range []string{
		"support-agent", "gpt-4o-mini", "deployed agent",
		"support-agent-turn-tests", "turn", "15 test cases", "source: agent",
		"support-agent-evaluator", "traces off",
		"contoso-project",
		"evals/azure.eval.yaml",
	} {
		assert.Contains(t, text, want,
			"the plan omits something the generation is about to do:\n%s", text)
	}
	assert.Contains(t, text, "2 artifact file(s)",
		"the local writes are named, because they are the half that is not billed")
}

// A conversation dataset holds seeds a simulator drives, so counting them as
// test cases promised fifteen ready-to-run comparisons that do not exist.
func TestTheGenerationPlanCountsScenariosForAConversationDataset(t *testing.T) {
	var out bytes.Buffer
	writeGenerationPlan(&out, generationSummary{
		model:      "gpt-4o-mini",
		configPath: "evals/azure.eval.yaml",
		plans: []generationPlan{{
			Kind: generateKindDataset, Name: "support-agent-conversation-tests",
			SampleSize: 15, EvaluationLevel: "conversation",
		}},
	})

	text := out.String()
	assert.Contains(t, text, "conversation · 15 scenarios", text)
	assert.NotContains(t, text, "test cases", text)
}

// Nothing detected is a fact about the generation, not an absence of one. A
// blank line here read as though instructions had never come up, and a run
// seeded by nothing is the one worth pausing over.
func TestTheGenerationPlanAdmitsWhenNothingSeededIt(t *testing.T) {
	var out bytes.Buffer
	writeGenerationPlan(&out, generationSummary{
		model:      "gpt-4o-mini",
		configPath: "evals/azure.eval.yaml",
		plans:      []generationPlan{{Kind: generateKindDataset, Name: "d"}},
	})

	assert.Contains(t, out.String(), "none detected", out.String())
}

// --no-wait bills the jobs and writes nothing, and the reader is owed that
// difference before they approve it rather than after they go looking for files.
func TestTheGenerationPlanSaysWhatNoWaitDefers(t *testing.T) {
	var out bytes.Buffer
	writeGenerationPlan(&out, generationSummary{
		model:      "gpt-4o-mini",
		configPath: "evals/azure.eval.yaml",
		noWait:     true,
		plans:      []generationPlan{{Kind: generateKindDataset, Name: "d"}},
	})

	text := out.String()
	assert.Contains(t, text, "nothing yet")
	assert.Contains(t, text, "--no-wait")
	assert.NotContains(t, text, "artifact file(s)",
		"promising files a --no-wait run will not write is the defect one step removed")
}

// A rubric seeded from traces costs a different thing than one that is not, so
// the plan says which.
func TestTheEvaluatorPlanSaysWhetherTracesSeedIt(t *testing.T) {
	var off, on bytes.Buffer
	writeGenerationPlan(&off, generationSummary{
		plans: []generationPlan{{Kind: generateKindEvaluator, Name: "e"}},
	})
	writeGenerationPlan(&on, generationSummary{
		plans: []generationPlan{{Kind: generateKindEvaluator, Name: "e", TraceDays: 7}},
	})

	assert.Contains(t, off.String(), "traces off")
	assert.Contains(t, on.String(), "7 day(s) of traces")
}

// The scope prompt is not asked of a caller who already answered it with a flag.
func TestGenerateScopeIsNotAskedWhenAFlagSaidIt(t *testing.T) {
	a := &generateAction{
		cmd:   noPromptCmd(t, false),
		flags: &generateCommandFlags{wantDataset: true},
	}

	got, err := a.askGenerateArtifacts()
	assert.NoError(t, err)
	assert.True(t, got.dataset)
	assert.False(t, got.evaluator,
		"reading their own flag back to them is not a question")
}

// With nobody to ask, both is what the command has always produced.
func TestGenerateScopeDefaultsToBothUnderNoPrompt(t *testing.T) {
	a := &generateAction{cmd: noPromptCmd(t, true), flags: &generateCommandFlags{}}

	got, err := a.askGenerateArtifacts()
	assert.NoError(t, err)
	assert.True(t, got.dataset)
	assert.True(t, got.evaluator)
}
