// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rubric file exists to be edited, and the part worth editing was the
// hardest part of it to find.
//
// The service returns its own wiring alongside the dimensions -- how the
// evaluator is initialized, what it reports, what columns it reads, and a
// prompt generated from the dimensions rather than authored. Left in, they
// outnumbered the dimensions several times over.
func TestTheWrittenRubricKeepsOnlyWhatIsWorthEditing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "support.json")

	require.NoError(t, writeRubric(path, json.RawMessage(`{
		"name": "support-evaluator",
		"definition": {
			"type": "rubric",
			"pass_threshold": 0.5,
			"dimensions": [{"name": "accuracy", "weight": 1, "description": "Is it right?"}],
			"init_parameters": {"model": "gpt-4o-mini"},
			"metrics": [{"name": "rubric_score"}],
			"data_schema": {"query": "string"},
			"prompt_text": "You are a grader..."
		}
	}`)))

	body, err := os.ReadFile(path) //nolint:gosec // this test's own temp dir
	require.NoError(t, err)
	text := string(body)

	assert.Contains(t, text, "accuracy")
	assert.Contains(t, text, "Is it right?")
	assert.Contains(t, text, "pass_threshold")
	for _, owned := range []string{"init_parameters", "metrics", "data_schema", "prompt_text"} {
		assert.NotContains(t, text, owned,
			"%s is the service's, not the reader's:\n%s", owned, text)
	}

	// And it is still a rubric the config can resolve.
	var back map[string]any
	require.NoError(t, json.Unmarshal(body, &back))
	assert.Equal(t, "rubric", back["type"])
}

// The file is committed and read in diffs, so its key order cannot come from
// Go's map iteration -- that rewrote the whole rubric on every regeneration
// whether or not anything about it had changed.
func TestTheWrittenRubricIsOrdered(t *testing.T) {
	payload := json.RawMessage(`{"definition":{
		"zeta": 1, "alpha": 2, "pass_threshold": 0.5, "type": "rubric",
		"dimensions": [{"name": "d"}]
	}}`)

	first := filepath.Join(t.TempDir(), "a.json")
	second := filepath.Join(t.TempDir(), "b.json")
	require.NoError(t, writeRubric(first, payload))
	require.NoError(t, writeRubric(second, payload))

	a, err := os.ReadFile(first) //nolint:gosec // this test's own temp dir
	require.NoError(t, err)
	b, err := os.ReadFile(second) //nolint:gosec // this test's own temp dir
	require.NoError(t, err)
	assert.Equal(t, string(a), string(b), "two writes of one rubric are one file")

	text := string(a)
	assert.Less(t, strings.Index(text, `"type"`), strings.Index(text, `"dimensions"`))
	assert.Less(t, strings.Index(text, `"dimensions"`), strings.Index(text, `"pass_threshold"`))
	assert.Less(t, strings.Index(text, `"pass_threshold"`), strings.Index(text, `"alpha"`),
		"anything the service adds later follows, sorted, rather than being dropped")
	assert.Contains(t, text, `"zeta"`, "and it is kept, because dropping it loses the artifact")
}

// A payload this does not understand is written whole. Losing a generated
// artifact is far worse than writing a wide one.
func TestAnUnrecognizedRubricPayloadIsWrittenWhole(t *testing.T) {
	dir := t.TempDir()

	noDimensions := filepath.Join(dir, "a.json")
	require.NoError(t, writeRubric(noDimensions, json.RawMessage(`{"definition":{"type":"prompt"}}`)))
	body, err := os.ReadFile(noDimensions) //nolint:gosec // this test's own temp dir
	require.NoError(t, err)
	assert.Contains(t, string(body), "prompt")

	noEnvelope := filepath.Join(dir, "b.json")
	require.NoError(t, writeRubric(noEnvelope, json.RawMessage(`{"something":"else"}`)))
	body, err = os.ReadFile(noEnvelope) //nolint:gosec // this test's own temp dir
	require.NoError(t, err)
	assert.Contains(t, string(body), "else")
}

// Nothing returned is not an empty rubric.
func TestAnEmptyRubricResultIsRefused(t *testing.T) {
	require.Error(t, writeRubric(filepath.Join(t.TempDir(), "a.json"), nil))
}
