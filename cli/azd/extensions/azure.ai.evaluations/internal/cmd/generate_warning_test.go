// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A warned generation must not read as a clean one.
//
// The artifact is already on disk and about to be declared, so the line that
// says to look at it first is the only thing standing between a thin rubric and
// a configuration that grades with it.
func TestAWarnedGenerationSaysSoBesideItsArtifact(t *testing.T) {
	var out bytes.Buffer
	job := &eval_api.GenerationJob{
		ID:     "evaluatorgen-1",
		Status: "succeeded",
		Warnings: []eval_api.JobWarning{
			{Code: "input_quality"},
		},
	}

	writeJobWarnings(&out, "evaluator", job, "evals/evaluators/support.json")

	text := out.String()
	assert.Contains(t, text, "input_quality", "the service's own code is what a search finds")
	assert.Contains(t, text, "insufficient or low quality",
		"and the code is explained: nobody reading it on a terminal knows what it means")
	assert.Contains(t, text, "evals/evaluators/support.json",
		"the file to look at is named, because it is already written by now")
}

// A job with nothing to say says nothing: an empty warning block reads as a
// problem the reader then goes looking for.
func TestACleanGenerationPrintsNoWarningBlock(t *testing.T) {
	var out bytes.Buffer
	writeJobWarnings(&out, "dataset",
		&eval_api.GenerationJob{ID: "datagen-1", Status: "succeeded"}, "evals/datasets/g.jsonl")
	assert.Empty(t, out.String())
}

// The JSON document carries the whole array, and still carries the artifact:
// a caller that only read the reference would take a warned generation for a
// clean one.
func TestWarningsReachTheJSONDocument(t *testing.T) {
	outcomes := []generationOutcome{{
		plan: generationPlan{Kind: generateKindEvaluator},
		ref:  nil,
		report: generationReport{
			jobID: "evaluatorgen-1",
			warnings: []eval_api.JobWarning{
				{Code: "input_quality", Message: "not enough rows"},
			},
		},
	}}

	raw, err := json.Marshal(generationDocument(outcomes))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	entry, ok := got[string(generateKindEvaluator)].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "evaluatorgen-1", entry["job_id"])

	warnings, ok := entry["warnings"].([]any)
	require.True(t, ok, "the complete array is preserved, not a summary of it")
	require.Len(t, warnings, 1)
	first, ok := warnings[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "input_quality", first["code"])
	assert.Equal(t, "not enough rows", first["message"])
}
