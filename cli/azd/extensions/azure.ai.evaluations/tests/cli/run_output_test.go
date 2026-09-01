// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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

	r := requireSuccess(t, run(t, "run", "output", "list", f.FirstRunID, "--eval", f.EvalID))

	require.Contains(t, r.Stdout, f.FirstRunID)
	require.Contains(t, r.Stdout, "Totals:")
	require.Contains(t, r.Stdout, "CRITERION")
	require.Contains(t, r.Stdout, f.EvaluatorName)

	// One row per evaluated sample, which is what makes "how many should I go
	// and look at" answerable by counting lines.
	for _, header := range []string{"ITEM", "SAMPLE", "FAILED EVALUATORS", "REASON"} {
		require.Containsf(t, r.Stdout, header, "the listing lost its %s column", header)
	}
	require.NotContains(t, r.Stdout, "EVALUATOR  ",
		"a per-verdict table would list a sample once per evaluator")

	// The fixture's rows all pass, so every row names no failing evaluator.
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

	// One rendered row is one evaluator's verdict on one sample, so the count
	// to expect is failing *results*, not failing rows: a sample that fails two
	// evaluators is two lines. `ResultCounts.Failed` answers the other question.
	failing := 0
	for _, item := range payload.OutputItems {
		for _, r := range item.Results {
			if !r.Passed {
				failing++
			}
		}
	}

	r := requireSuccess(t, run(t, "run", "output", "list", f.FirstRunID,
		"--eval", f.EvalID, "--failed-only"))

	if failing == 0 {
		// Saying so is not the same as printing an empty table.
		require.Contains(t, r.Stdout, "No failing rows.")
		return
	}

	require.NotContains(t, r.Stdout, " pass ",
		"--failed-only must drop the rows that passed")

	// Matched on a word boundary so the per-criterion table's FAILED column
	// header is not counted as a verdict.
	verdicts := regexp.MustCompile(`\bFAIL\b`).FindAllString(r.Stdout, -1)
	require.Equal(t, failing, len(verdicts),
		"every failing verdict must appear exactly once:\n%s", r.Stdout)
}

// resultsFor reads a run's results as JSON, which several tests need before
// they can decide what the rendered output should say.
func resultsFor(t *testing.T, evalID, runID string) resultsPayload {
	t.Helper()
	r := requireSuccess(t, run(t, "run", "output", "list", runID, "--eval", evalID, "-o", "json"))
	var payload resultsPayload
	r.JSON(t, &payload)
	return payload
}

// exportedDocument is what `run output export` writes: the run as the service
// returned it, and every output item under it. The flat formats are gone, so
// one document is the whole contract.
type exportedDocument struct {
	Run struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		ResultCounts struct {
			Total  int `json:"total"`
			Passed int `json:"passed"`
			Failed int `json:"failed"`
		} `json:"result_counts"`
	} `json:"run"`
	Items []map[string]any `json:"items"`
}

func TestCLIResultsExport(t *testing.T) {
	f := sharedEval(t)

	t.Run("json to stdout carries the run and every item", func(t *testing.T) {
		r := requireSuccess(t, run(t, "run", "output", "export", f.FirstRunID,
			"--eval", f.EvalID, "--format", "json"))

		var exported exportedDocument
		r.JSON(t, &exported)
		require.Equal(t, f.FirstRunID, exported.Run.ID)
		require.Equal(t, "completed", exported.Run.Status)
		require.Equal(t, len(fixtureQueries), exported.Run.ResultCounts.Total)
		// The export exists to be complete: a run summary without its items is
		// the partial export this replaced.
		require.Len(t, exported.Items, len(fixtureQueries),
			"the export must carry every output item, not just the run")
	})

	t.Run("json is the default, so --format is optional", func(t *testing.T) {
		r := requireSuccess(t, run(t, "run", "output", "export", f.FirstRunID,
			"--eval", f.EvalID))

		var exported exportedDocument
		r.JSON(t, &exported)
		require.Equal(t, f.FirstRunID, exported.Run.ID)
	})

	t.Run("output-file writes the path instead of stdout", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "results.json")

		r := requireSuccess(t, runIn(t, dir, "run", "output", "export", f.FirstRunID,
			"--eval", f.EvalID, "--output-file", path))
		require.Empty(t, strings.TrimSpace(r.Stdout),
			"--output-file redirects the payload; leaving it on stdout too would double it")

		body, err := os.ReadFile(path)
		require.NoError(t, err)
		// --output-file changes where the payload goes, not what it is.
		var exported exportedDocument
		require.NoError(t, json.Unmarshal(body, &exported))
		require.Equal(t, f.FirstRunID, exported.Run.ID)
		require.Len(t, exported.Items, len(fixtureQueries))
	})

	t.Run("an unknown format is refused", func(t *testing.T) {
		r := requireFailure(t, run(t, "run", "output", "export", f.FirstRunID,
			"--eval", f.EvalID, "--format", "xml"))
		require.Contains(t, r.Combined(), `--format "xml" is not supported`)
		// The refusal has to name what the exporter can actually write, or it
		// repeats the bug where the guard and the exporter disagreed.
		require.Contains(t, r.Combined(), "use json")
	})

	t.Run("the formats that were dropped are refused, not silently ignored", func(t *testing.T) {
		for _, format := range []string{"csv", "jsonl"} {
			t.Run(format, func(t *testing.T) {
				r := requireFailure(t, run(t, "run", "output", "export", f.FirstRunID,
					"--eval", f.EvalID, "--format", format))
				require.Contains(t, r.Combined(), `is not supported`)
			})
		}
	})
}

func TestCLIResultsUnknownEvalIsBrief(t *testing.T) {
	r := requireFailure(t, run(t, "run", "output", "list", "--eval", "eval_does_not_exist"))
	require.Contains(t, r.Combined(), "eval_does_not_exist")
	require.NotContains(t, r.Combined(), "RESPONSE 404")
}
