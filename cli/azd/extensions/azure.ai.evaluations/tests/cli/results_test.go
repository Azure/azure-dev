// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The fixture is judged by a model, so no test here may assert how many rows
// passed. What is under test is the command, and the properties that hold
// whatever the judge decided: every dataset row comes back, every row carries a
// verdict and a score, and filtering by verdict returns a subset that agrees
// with the totals.

// resultsPayload is what `results show -o json` emits: the run and the rows.
type resultsPayload struct {
	Run struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		ResultCounts struct {
			Total   int `json:"total"`
			Passed  int `json:"passed"`
			Failed  int `json:"failed"`
			Errored int `json:"errored"`
		} `json:"result_counts"`
		PerTestingCriteria []struct {
			TestingCriteria string `json:"testing_criteria"`
			Passed          int    `json:"passed"`
			Failed          int    `json:"failed"`
		} `json:"per_testing_criteria_results"`
	} `json:"run"`
	OutputItems []struct {
		ID             string         `json:"id"`
		Status         string         `json:"status"`
		DataSourceItem map[string]any `json:"datasource_item"`
		Results        []struct {
			Name   string   `json:"name"`
			Score  *float64 `json:"score"`
			Passed bool     `json:"passed"`
		} `json:"results"`
	} `json:"output_items"`
}

// TestCLIResultsShowRendersTheRows is the difference between `results show` and
// `run show`: the totals say how many failed, these say which.
func TestCLIResultsShowRendersTheRows(t *testing.T) {
	f := sharedEval(t)

	r := requireSuccess(t, run(t, "results", "show", f.EvalID, "--run-id", f.FirstRunID))

	require.Contains(t, r.Stdout, f.FirstRunID)
	require.Contains(t, r.Stdout, "Totals:")
	require.Contains(t, r.Stdout, "CRITERION")
	require.Contains(t, r.Stdout, "ITEM")
	require.Contains(t, r.Stdout, "EVALUATOR")
	require.Contains(t, r.Stdout, "SCORE")
	require.Contains(t, r.Stdout, f.EvaluatorName)

	// The row's own input, so that a table printing only counts would not
	// satisfy every assertion above.
	require.Contains(t, r.Stdout, "query=")
	require.Contains(t, r.Stdout, "Report:")
}

func TestCLIResultsShowJSON(t *testing.T) {
	f := sharedEval(t)

	payload := resultsFor(t, f.EvalID, f.FirstRunID)

	require.Equal(t, f.FirstRunID, payload.Run.ID)
	require.Equal(t, "completed", payload.Run.Status)
	require.Equal(t, len(fixtureQueries), payload.Run.ResultCounts.Total)
	require.Zero(t, payload.Run.ResultCounts.Errored,
		"an errored row means the fixture measured nothing")

	require.Len(t, payload.Run.PerTestingCriteria, 1)
	require.Equal(t, f.EvaluatorName, payload.Run.PerTestingCriteria[0].TestingCriteria)

	// The rows are the reason this command exists, and a run reporting counts
	// while returning none would still satisfy everything above.
	require.Len(t, payload.OutputItems, len(fixtureQueries),
		"every dataset row must come back as an item")

	passed := 0
	for _, item := range payload.OutputItems {
		require.NotEmpty(t, item.DataSourceItem["query"],
			"each row must carry the column it was evaluated on")
		require.Len(t, item.Results, 1)
		require.Equal(t, f.EvaluatorName, item.Results[0].Name)
		require.NotNil(t, item.Results[0].Score, "a scored row must report its score")
		if item.Results[0].Passed {
			passed++
		}
	}
	require.Equal(t, payload.Run.ResultCounts.Passed, passed,
		"the per-row verdicts must agree with the totals")
}

// TestCLIResultsShowFailedOnly asserts the filter removes rows rather than
// merely relabelling them.
//
// The service has no verdict filter — its `status` selects on execution status,
// so `status=failed` returns errored rows, not failing ones — which makes this
// entirely the CLI's own work and worth testing directly.
func TestCLIResultsShowFailedOnly(t *testing.T) {
	f := sharedEval(t)

	payload := resultsFor(t, f.EvalID, f.FirstRunID)
	failed := payload.Run.ResultCounts.Failed

	r := requireSuccess(t, run(t, "results", "show", f.EvalID,
		"--run-id", f.FirstRunID, "--failed-only"))

	if failed == 0 {
		// Saying so is not the same as printing an empty table.
		require.Contains(t, r.Stdout, "No failing rows.")
		return
	}

	require.NotContains(t, r.Stdout, " pass ",
		"--failed-only must drop the rows that passed")
	require.Contains(t, r.Stdout, "FAIL")
	require.Equal(t, failed, strings.Count(r.Stdout, "FAIL"),
		"every failing row must appear exactly once")
}

// resultsFor reads a run's results as JSON, which several tests need before
// they can decide what the rendered output should say.
func resultsFor(t *testing.T, evalID, runID string) resultsPayload {
	t.Helper()
	r := requireSuccess(t, run(t, "results", "show", evalID, "--run-id", runID, "-o", "json"))
	var payload resultsPayload
	r.JSON(t, &payload)
	return payload
}

func TestCLIResultsExport(t *testing.T) {
	f := sharedEval(t)

	t.Run("json to stdout", func(t *testing.T) {
		r := requireSuccess(t, run(t, "results", "export", f.EvalID,
			"--run-id", f.FirstRunID, "--format", "json"))

		var exported struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			ResultCounts struct {
				Total  int `json:"total"`
				Passed int `json:"passed"`
				Failed int `json:"failed"`
			} `json:"result_counts"`
		}
		r.JSON(t, &exported)
		require.Equal(t, f.FirstRunID, exported.ID)
		require.Equal(t, "completed", exported.Status)
		require.Equal(t, len(fixtureQueries), exported.ResultCounts.Total)
	})

	t.Run("csv to stdout", func(t *testing.T) {
		r := requireSuccess(t, run(t, "results", "export", f.EvalID,
			"--run-id", f.FirstRunID, "--format", "csv"))

		rows, err := csv.NewReader(strings.NewReader(r.Stdout)).ReadAll()
		require.NoError(t, err, "--format csv must emit parseable CSV:\n%s", r.Stdout)
		require.Len(t, rows, 2, "a header and one row per criterion")
		require.Equal(t,
			[]string{"run_id", "status", "criterion", "passed", "failed"}, rows[0])
		require.Equal(t, f.FirstRunID, rows[1][0])
		require.Equal(t, "completed", rows[1][1])
		require.Equal(t, f.EvaluatorName, rows[1][2])
	})

	t.Run("out-file writes the path instead of stdout", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "results.csv")

		r := requireSuccess(t, runIn(t, dir, "results", "export", f.EvalID,
			"--run-id", f.FirstRunID, "--format", "csv", "-O", path))
		require.Empty(t, strings.TrimSpace(r.Stdout),
			"-O redirects the payload; leaving it on stdout too would double it")

		body, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(body), "run_id,status,criterion,passed,failed")
		require.Contains(t, string(body), f.FirstRunID)
	})

	t.Run("an unknown format is refused", func(t *testing.T) {
		r := requireFailure(t, run(t, "results", "export", f.EvalID,
			"--run-id", f.FirstRunID, "--format", "xml"))
		require.Contains(t, r.Combined(), "json or csv")
	})
}

func TestCLIResultsUnknownEvalIsBrief(t *testing.T) {
	r := requireFailure(t, run(t, "results", "show", "eval_does_not_exist"))
	require.Contains(t, r.Combined(), "eval_does_not_exist")
	require.NotContains(t, r.Combined(), "RESPONSE 404")
}
