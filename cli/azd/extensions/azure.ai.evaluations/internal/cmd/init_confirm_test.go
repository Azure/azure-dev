// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The summary is what the reader approves, so every value the scaffold is
// about to write has to be in it.
//
// init used to write both files and then report, so the first time anyone saw
// which agent, dataset and evaluators it had settled on, the settling was
// already on disk.
func TestScaffoldSummaryStatesWhatWillBeWritten(t *testing.T) {
	var out bytes.Buffer
	writeScaffoldSummary(&out, scaffoldSummary{
		configPath: "evals/azure.eval.yaml",
		wiring:     wiringAdded,
		answers: initAnswers{
			evalName:        "support-agent-dataset-eval",
			target:          "support-agent",
			source:          initSourceDataset,
			datasetRef:      "support-regression",
			evaluationLevel: project.EvaluationLevelTurn,
			judgeModel:      "gpt-4o-mini",
			evaluators:      []string{"builtin.task_completion"},
		},
	})

	text := out.String()
	for _, want := range []string{
		"support-agent-dataset-eval", "support-agent", "dataset",
		"support-regression", "turn", "gpt-4o-mini", "builtin.task_completion",
		"evals/azure.eval.yaml",
	} {
		assert.Contains(t, text, want, "the summary omits a value it is about to write:\n%s", text)
	}
	assert.Contains(t, text, "add evaluation service",
		"the reader is approving an edit to a file they did not name, so it is listed")
	assert.NotContains(t, text, "Window",
		"a dataset-backed eval reads no traces, so it has no window to report")
}

// A trace-backed eval reports the two things that decide which conversations
// it graded, and neither is a dataset.
func TestScaffoldSummaryReportsTheTraceWindow(t *testing.T) {
	var out bytes.Buffer
	writeScaffoldSummary(&out, scaffoldSummary{
		configPath: "evals/azure.eval.yaml",
		wiring:     wiringPresent,
		maxTraces:  20,
		answers: initAnswers{
			evalName: "support-agent-trace-eval", target: "support-agent",
			source: initSourceTraces, lookbackHours: 168,
			evaluationLevel: project.EvaluationLevelTurn,
		},
	})

	text := out.String()
	assert.Contains(t, text, "last 7 days", "the window is stated as it was offered, not as 168")
	assert.Contains(t, text, "20")
	assert.NotContains(t, text, "Dataset:")
	assert.Contains(t, text, "unchanged (evaluation service already exists)",
		"claiming an edit that will not happen is the same defect as hiding one that will")
}

// The window reads the way the prompt asked it.
func TestTraceWindowSummaryReadsBackAsOffered(t *testing.T) {
	assert.Equal(t, "last 24 hours", messages.TraceWindowSummary(24))
	assert.Equal(t, "last 7 days", messages.TraceWindowSummary(168))
	assert.Equal(t, "last 30 days", messages.TraceWindowSummary(720))
	assert.Equal(t, "service default", messages.TraceWindowSummary(0),
		"no window written is not a window of zero")
}

// A project that already carries the service is not edited again, and one that
// carries something else under that name is refused rather than replaced.
func TestRootEvalServiceAction(t *testing.T) {
	proj := func(services map[string]*azdext.ServiceConfig) *azdext.ProjectConfig {
		return &azdext.ProjectConfig{Path: "/proj", Services: services}
	}

	action, err := rootEvalServiceAction(proj(nil), "a-evals", "/proj/evals/azure.eval.yaml")
	require.NoError(t, err)
	assert.Equal(t, wiringAdded, action)

	// AddService assigns by name, so a service this extension does not own
	// would be replaced rather than added to.
	_, err = rootEvalServiceAction(
		proj(map[string]*azdext.ServiceConfig{"a-evals": {Host: "containerapp"}}),
		"a-evals", "/proj/evals/azure.eval.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a-evals")
}
