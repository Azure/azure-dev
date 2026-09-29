// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A job the service qualified is not a clean one.
//
// Warnings were decoded nowhere, so `input_quality` -- the service saying the
// input it was given was too thin to generate from -- arrived as an unqualified
// success, and the artifact went into a configuration with nothing saying to
// look at it first.
func TestWarningsSurviveTheTopLevelShape(t *testing.T) {
	var job GenerationJob
	require.NoError(t, json.Unmarshal([]byte(`{
		"id": "evaluatorgen-1",
		"status": "succeeded",
		"warnings": [{"code": "input_quality", "message": "not enough rows"}]
	}`), &job))

	got := job.AllWarnings()
	require.Len(t, got, 1)
	assert.Equal(t, "input_quality", got[0].Code)
	assert.Equal(t, "not enough rows", got[0].Message)
}

// The service has also put them inside the result envelope, and reading only
// one place reports a warned job as a clean one.
func TestWarningsSurviveTheResultShapes(t *testing.T) {
	nested := GenerationJob{Result: json.RawMessage(
		`{"name":"e","warnings":["input_quality"]}`)}
	require.Len(t, nested.AllWarnings(), 1)
	assert.Equal(t, "input_quality", nested.AllWarnings()[0].Code,
		"a bare string is the code; binding only the object shape produced an empty one")

	inOutputs := GenerationJob{Result: json.RawMessage(
		`{"outputs":[{"name":"e","warnings":[{"code":"input_quality"}]}]}`)}
	require.Len(t, inOutputs.AllWarnings(), 1)
	assert.Equal(t, "input_quality", inOutputs.AllWarnings()[0].Code)
}

// One warning reported in two places is one warning.
func TestWarningsAreNotDoubleCounted(t *testing.T) {
	job := GenerationJob{
		Warnings: []JobWarning{{Code: "input_quality"}},
		Result:   json.RawMessage(`{"warnings":["input_quality"]}`),
	}
	assert.Len(t, job.AllWarnings(), 1)
}

// An empty entry is not a warning, and reporting it puts "(!) generated with
// warning:" on screen with nothing after it.
func TestEmptyWarningsAreDropped(t *testing.T) {
	job := GenerationJob{Warnings: []JobWarning{{}, {Code: "  "}}}
	assert.Empty(t, job.AllWarnings())

	clean := GenerationJob{ID: "x", Status: "succeeded"}
	assert.Empty(t, clean.AllWarnings(), "a job with nothing to say says nothing")
}
