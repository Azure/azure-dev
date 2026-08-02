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
//
// The fixture's baseline run scores one row and fails two, so the rendering is
// checked against known verdicts rather than against whatever came back.
func TestCLIResultsShowRendersTheRows(t *testing.T) {
	f := sharedEval(t)

	r := requireSuccess(t, run(t, "results", "show", f.EvalID, "--run-id", f.BaselineRunID))

	require.Contains(t, r.Stdout, f.BaselineRunID)
	require.Contains(t, r.Stdout, "Totals: 1 passed, 2 failed, 0 errored")
	require.Contains(t, r.Stdout, "CRITERION")
	require.Contains(t, r.Stdout, "ITEM")
	require.Contains(t, r.Stdout, "EVALUATOR")
	require.Contains(t, r.Stdout, "SCORE")
	require.Contains(t, r.Stdout, f.EvaluatorName)

	// Both verdicts, and the row's own input: a table that showed only the
	// counts would satisfy every assertion above.
	require.Contains(t, r.Stdout, "FAIL")
	require.Contains(t, r.Stdout, "pass")
	require.Contains(t, r.Stdout, "response=a good answer")
	require.Contains(t, r.Stdout, "response=a bad answer")
	require.Contains(t, r.Stdout, "Report:")
}

func TestCLIResultsShowJSON(t *testing.T) {
	f := sharedEval(t)

	r := requireSuccess(t, run(t, "results", "show", f.EvalID,
		"--run-id", f.BaselineRunID, "-o", "json"))

	var payload resultsPayload
	r.JSON(t, &payload)

	require.Equal(t, f.BaselineRunID, payload.Run.ID)
	require.Equal(t, "completed", payload.Run.Status)
	require.Equal(t, 3, payload.Run.ResultCounts.Total)
	require.Equal(t, 1, payload.Run.ResultCounts.Passed)
	require.Equal(t, 2, payload.Run.ResultCounts.Failed)
	require.Zero(t, payload.Run.ResultCounts.Errored)

	require.Len(t, payload.Run.PerTestingCriteria, 1)
	require.Equal(t, f.EvaluatorName, payload.Run.PerTestingCriteria[0].TestingCriteria)

	// The rows are the reason this command exists, and a run reporting counts
	// while returning none would still satisfy everything above.
	require.Len(t, payload.OutputItems, 3, "every dataset row must come back as an item")

	passed := 0
	for _, item := range payload.OutputItems {
		require.NotEmpty(t, item.DataSourceItem["response"],
			"each row must carry the column it was evaluated on")
		require.Len(t, item.Results, 1)
		require.Equal(t, f.EvaluatorName, item.Results[0].Name)
		require.NotNil(t, item.Results[0].Score, "a scored row must report its score")
		if item.Results[0].Passed {
			passed++
			require.Equal(t, 1.0, *item.Results[0].Score)
		} else {
			require.Equal(t, 0.0, *item.Results[0].Score)
		}
	}
	require.Equal(t, 1, passed, "the per-row verdicts must agree with the totals")
}

// TestCLIResultsShowFailedOnly asserts the filter removes rows rather than
// merely relabelling them.
func TestCLIResultsShowFailedOnly(t *testing.T) {
	f := sharedEval(t)

	r := requireSuccess(t, run(t, "results", "show", f.EvalID,
		"--run-id", f.BaselineRunID, "--failed-only"))

	require.Contains(t, r.Stdout, "response=a bad answer")
	require.NotContains(t, r.Stdout, "response=a good answer",
		"--failed-only must drop the rows that passed")

	// The passing run has nothing to show, and saying so is not the same as
	// printing an empty table.
	empty := requireSuccess(t, run(t, "results", "show", f.EvalID,
		"--run-id", f.TreatmentRunID, "--failed-only"))
	require.Contains(t, empty.Stdout, "No failing rows.")
}

func TestCLIResultsExport(t *testing.T) {
	f := sharedEval(t)

	t.Run("json to stdout", func(t *testing.T) {
		r := requireSuccess(t, run(t, "results", "export", f.EvalID,
			"--run-id", f.BaselineRunID, "--format", "json"))

		var exported struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			ResultCounts struct {
				Passed int `json:"passed"`
				Failed int `json:"failed"`
			} `json:"result_counts"`
		}
		r.JSON(t, &exported)
		require.Equal(t, f.BaselineRunID, exported.ID)
		require.Equal(t, "completed", exported.Status)
		require.Equal(t, 1, exported.ResultCounts.Passed)
		require.Equal(t, 2, exported.ResultCounts.Failed)
	})

	t.Run("csv to stdout", func(t *testing.T) {
		r := requireSuccess(t, run(t, "results", "export", f.EvalID,
			"--run-id", f.BaselineRunID, "--format", "csv"))

		rows, err := csv.NewReader(strings.NewReader(r.Stdout)).ReadAll()
		require.NoError(t, err, "--format csv must emit parseable CSV:\n%s", r.Stdout)
		require.Len(t, rows, 2, "a header and one row per criterion")
		require.Equal(t,
			[]string{"run_id", "status", "criterion", "passed", "failed"}, rows[0])
		require.Equal(t,
			[]string{f.BaselineRunID, "completed", f.EvaluatorName, "1", "2"}, rows[1])
	})

	t.Run("out-file writes the path instead of stdout", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "results.csv")

		r := requireSuccess(t, runIn(t, dir, "results", "export", f.EvalID,
			"--run-id", f.BaselineRunID, "--format", "csv", "-O", path))
		require.Empty(t, strings.TrimSpace(r.Stdout),
			"-O redirects the payload; leaving it on stdout too would double it")

		body, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(body), "run_id,status,criterion,passed,failed")
		require.Contains(t, string(body), f.BaselineRunID)
	})

	t.Run("an unknown format is refused", func(t *testing.T) {
		r := requireFailure(t, run(t, "results", "export", f.EvalID,
			"--run-id", f.BaselineRunID, "--format", "xml"))
		require.Contains(t, r.Combined(), "json or csv")
	})
}

// comparison is the shape `results compare -o json` emits.
type comparison struct {
	State   string `json:"state"`
	Request struct {
		EvalID          string   `json:"evalId"`
		BaselineRunID   string   `json:"baselineRunId"`
		TreatmentRunIDs []string `json:"treatmentRunIds"`
	} `json:"request"`
	Result struct {
		Method      string `json:"method"`
		Comparisons []struct {
			TestingCriteria    string `json:"testingCriteria"`
			Metric             string `json:"metric"`
			BaselineRunSummary struct {
				RunID       string  `json:"runId"`
				SampleCount int     `json:"sampleCount"`
				Average     float64 `json:"average"`
			} `json:"baselineRunSummary"`
			CompareItems []struct {
				TreatmentRunSummary struct {
					RunID       string  `json:"runId"`
					SampleCount int     `json:"sampleCount"`
					Average     float64 `json:"average"`
				} `json:"treatmentRunSummary"`
				DeltaEstimate   float64 `json:"deltaEstimate"`
				TreatmentEffect string  `json:"treatmentEffect"`
			} `json:"compareItems"`
		} `json:"comparisons"`
	} `json:"result"`
}

// TestCLIResultsCompare needs two completed runs of the same eval that scored
// differently, which is why the fixture seeds one run to fail two of three
// rows: comparing two identical runs reports a zero delta, and a comparison
// that computed nothing would look the same.
func TestCLIResultsCompare(t *testing.T) {
	f := sharedEval(t)

	t.Run("rendered columns", func(t *testing.T) {
		r := requireSuccess(t, run(t, "results", "compare", f.EvalID,
			"--baseline", f.BaselineRunID, "--treatment", f.TreatmentRunID))

		for _, header := range []string{
			"METRIC", "TREATMENT RUN", "BASELINE", "TREATMENT", "DELTA", "P-VALUE", "EFFECT",
		} {
			require.Containsf(t, r.Stdout, header, "the comparison table lost its %s column", header)
		}
		require.Contains(t, r.Stdout, "Method:")
		require.Contains(t, r.Stdout, f.TreatmentRunID)
		require.Contains(t, r.Stdout, f.EvaluatorName)

		// One in three against three in three. The delta is signed, which is
		// the whole point of naming a baseline.
		require.Contains(t, r.Stdout, "0.333")
		require.Contains(t, r.Stdout, "1.000")
		require.Contains(t, r.Stdout, "+0.667")
	})

	t.Run("json shape", func(t *testing.T) {
		r := requireSuccess(t, run(t, "results", "compare", f.EvalID,
			"--baseline", f.BaselineRunID, "--treatment", f.TreatmentRunID, "-o", "json"))

		var got comparison
		r.JSON(t, &got)

		require.Equal(t, "Succeeded", got.State)
		require.Equal(t, f.EvalID, got.Request.EvalID)
		require.Equal(t, f.BaselineRunID, got.Request.BaselineRunID)
		require.Equal(t, []string{f.TreatmentRunID}, got.Request.TreatmentRunIDs)

		require.NotEmpty(t, got.Result.Method)
		require.Len(t, got.Result.Comparisons, 1)
		c := got.Result.Comparisons[0]
		require.Equal(t, f.EvaluatorName, c.TestingCriteria)
		require.Equal(t, f.BaselineRunID, c.BaselineRunSummary.RunID)
		require.Equal(t, 3, c.BaselineRunSummary.SampleCount)
		require.InDelta(t, 1.0/3.0, c.BaselineRunSummary.Average, 0.001)

		require.Len(t, c.CompareItems, 1)
		item := c.CompareItems[0]
		require.Equal(t, f.TreatmentRunID, item.TreatmentRunSummary.RunID)
		require.Equal(t, 1.0, item.TreatmentRunSummary.Average)
		require.InDelta(t, 2.0/3.0, item.DeltaEstimate, 0.001)
		require.NotEmpty(t, item.TreatmentEffect,
			"the effect classifies the result, including when there are too few samples")
	})

	// Naming neither run is the common case — "did my last change help?" — so
	// the defaults are asserted against the listing rather than against the
	// fixture's own ids, which is what the command itself resolves from.
	t.Run("defaults to the two most recent completed runs", func(t *testing.T) {
		listed := requireSuccess(t, run(t, "run", "list", f.EvalID, "-o", "json"))
		var runs []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		listed.JSON(t, &runs)

		completed := make([]string, 0, len(runs))
		for _, r := range runs {
			if r.Status == "completed" {
				completed = append(completed, r.ID)
			}
		}
		require.GreaterOrEqual(t, len(completed), 2,
			"comparing needs two completed runs of the same eval")

		r := requireSuccess(t, run(t, "results", "compare", f.EvalID, "-o", "json"))
		var got comparison
		r.JSON(t, &got)

		require.Equal(t, completed[0], got.Request.TreatmentRunIDs[0],
			"the treatment defaults to the most recent completed run")
		require.Equal(t, completed[1], got.Request.BaselineRunID,
			"the baseline defaults to the one before it")
		require.Equal(t, "Succeeded", got.State)
	})
}

// TestCLIResultsUnknownEvalIsBrief covers the failure a user hits by typo. The
// service answers with a long JSON body; printing it verbatim buries the one
// useful sentence.
func TestCLIResultsUnknownEvalIsBrief(t *testing.T) {
	r := requireFailure(t, run(t, "results", "show", "eval_azdcli_does_not_exist"))
	require.Less(t, len(r.Combined()), 600,
		"a not-found must stay short, not dump the service body:\n%s", r.Combined())
	require.Contains(t, r.Combined(), "eval_azdcli_does_not_exist")
}
