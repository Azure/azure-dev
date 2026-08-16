// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twoCriteriaRun is a finished run with the shape export has to preserve: one
// row per testing criterion, all carrying the run they belong to.
func twoCriteriaRun() *eval_api.OpenAIEvalRun {
	return &eval_api.OpenAIEvalRun{
		ID:     "evalrun_abc",
		Status: "completed",
		PerTestingCriteria: []eval_api.EvalRunCriteriaResult{
			{TestingCriteria: "task_adherence", Passed: 8, Failed: 2},
			{TestingCriteria: "coherence", Passed: 10, Failed: 0},
		},
	}
}

// An export is read by a spreadsheet or a diff, so the header is part of the
// contract: renaming a column silently breaks whatever consumes it.
func TestWriteResultsCSV(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeResultsCSV(&buf, twoCriteriaRun()))

	rows, err := csv.NewReader(&buf).ReadAll()
	require.NoError(t, err)

	assert.Equal(t, []string{"run_id", "status", "testing_criteria", "passed", "failed"}, rows[0])
	assert.Equal(t, []string{"evalrun_abc", "completed", "task_adherence", "8", "2"}, rows[1])
	assert.Equal(t, []string{"evalrun_abc", "completed", "coherence", "10", "0"}, rows[2])
	assert.Len(t, rows, 3, "one header and one row per criterion")
}

// A run that graded nothing still has to produce a file with a header, because
// a consumer that gets zero bytes cannot tell an empty run from a failed
// export.
func TestWriteResultsCSV_RunWithNoCriteria(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeResultsCSV(&buf, &eval_api.OpenAIEvalRun{
		ID: "evalrun_empty", Status: "failed",
	}))

	rows, err := csv.NewReader(&buf).ReadAll()
	require.NoError(t, err)

	require.Len(t, rows, 2)
	assert.Equal(t, []string{"run_id", "status", "testing_criteria", "passed", "failed"}, rows[0])
	assert.Equal(t, []string{"evalrun_empty", "failed", "", "", ""}, rows[1])
}

// A criterion name is service-supplied, so it can hold anything. The writer
// has to quote rather than corrupt the row.
func TestWriteResultsCSV_QuotesASeparatorInTheData(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeResultsCSV(&buf, &eval_api.OpenAIEvalRun{
		ID:     "evalrun_abc",
		Status: "completed",
		PerTestingCriteria: []eval_api.EvalRunCriteriaResult{
			{TestingCriteria: `groundedness, strict`, Passed: 1, Failed: 0},
		},
	}))

	rows, err := csv.NewReader(&buf).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "groundedness, strict", rows[1][2],
		"a comma in a criterion name must survive the round trip")
}

// One criterion per line is what lets a downstream job stream results without
// holding the whole run.
func TestWriteResultsJSONL(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeResultsJSONL(&buf, twoCriteriaRun()))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2, "one line per criterion")

	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	assert.Equal(t, "evalrun_abc", first["run_id"])
	assert.Equal(t, "completed", first["status"])
	assert.Equal(t, "task_adherence", first["testing_criteria"])
	assert.EqualValues(t, 8, first["passed"])
	assert.EqualValues(t, 2, first["failed"])

	var second map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &second))
	assert.Equal(t, "coherence", second["testing_criteria"])
}

// Every line has to parse on its own; that is the whole point of the format.
func TestWriteResultsJSONL_EachLineParsesAlone(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeResultsJSONL(&buf, twoCriteriaRun()))

	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		var row map[string]any
		assert.NoErrorf(t, json.Unmarshal([]byte(line), &row), "line is not self-contained: %s", line)
	}
}

func TestWriteResultsJSONL_RunWithNoCriteria(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeResultsJSONL(&buf, &eval_api.OpenAIEvalRun{
		ID: "evalrun_empty", Status: "failed",
	}))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 1)

	var row map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &row))
	assert.Equal(t, "evalrun_empty", row["run_id"])
	assert.Equal(t, "failed", row["status"])
	assert.NotContains(t, row, "testing_criteria",
		"a run that graded nothing must not claim a criterion")
}

// The three export formats are a documented set. A fourth spelling, or a
// missing one, is a promise broken on either side.
func TestExportFormatsAreTheDocumentedSet(t *testing.T) {
	assert.Equal(t, "csv", formatCSV)
	assert.Equal(t, "json", formatJSON)
	assert.Equal(t, "jsonl", formatJSONL)

	usage := find(t, "run output export").Flags().Lookup("format")
	require.NotNil(t, usage)
	assert.Equal(t, formatCSV, usage.DefValue,
		"results are a table, so the default artifact is the one a spreadsheet opens")

	for _, f := range []string{formatCSV, formatJSON, formatJSONL} {
		assert.Containsf(t, usage.Usage, f, "--format accepts %q, so its help has to say so", f)
	}
}
