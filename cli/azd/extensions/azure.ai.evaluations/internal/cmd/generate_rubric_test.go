// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The generated rubric is the file the next deploy compares against, so a field
// dropped on the way to disk republishes the evaluator without it. pass_threshold
// is what decides pass or fail, so losing it changes grading silently.
func TestWriteRubricKeepsTheWholeDefinition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rubric.json")

	result := json.RawMessage(`{
      "name": "support-agent-quality",
      "version": "1",
      "definition": {
        "type": "rubric",
        "pass_threshold": 0.5,
        "dimensions": [{"id": "accuracy", "description": "Correct.", "weight": 9}],
        "something_the_service_added_later": true
      }
    }`)

	require.NoError(t, writeRubric(path, result))

	var got map[string]any
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &got))

	assert.Equal(t, 0.5, got["pass_threshold"],
		"the threshold decides pass or fail, so losing it changes grading")
	assert.Equal(t, true, got["something_the_service_added_later"],
		"the definition is written through, so a new field is not lost either")
	assert.Equal(t, "rubric", got["type"])
	assert.Len(t, got["dimensions"], 1)
	assert.NotContains(t, got, "name", "only the definition is written, not the envelope")
}

// A payload that is not a rubric is kept verbatim rather than discarded.
func TestWriteRubricFallsBackToTheRawPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rubric.json")
	require.NoError(t, writeRubric(path, json.RawMessage(`{"unexpected":"shape"}`)))

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.JSONEq(t, `{"unexpected":"shape"}`, string(body))
}
