// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const failureReasonText = "the judge model deployment was not found"

// The reason a run failed has to reach the person reading it wherever the
// service put it. A failed run whose summary says only "failed" sends the
// reader to the portal for the one thing the response already held.
func TestFailedRunSummaryNamesTheReasonWhereverItIsReported(t *testing.T) {
	for name, errorBody := range map[string]string{
		"message": `{"code":"InitializationFailed","message":"` + failureReasonText + `"}`,
		"details array": `{"code":"InitializationFailed","message":"","details":[` +
			`{"message":"` + failureReasonText + `"}]}`,
		"azure innererror":     `{"code":"InitializationFailed","innererror":{"message":"` + failureReasonText + `"}}`,
		"snake case inner":     `{"inner_error":{"message":"` + failureReasonText + `"}}`,
		"openai error wrapper": `{"error":{"message":"` + failureReasonText + `"}}`,
		"message is an object": `{"message":{"message":"` + failureReasonText + `"}}`,
		"bare string":          `"` + failureReasonText + `"`,
	} {
		t.Run(name, func(t *testing.T) {
			var run eval_api.OpenAIEvalRun
			require.NoError(t, json.Unmarshal(
				[]byte(`{"id":"run_failed","eval_id":"eval_failed","status":"failed","error":`+errorBody+`}`), &run))
			for caller, render := range map[string]func(*bytes.Buffer) error{
				"summary": func(out *bytes.Buffer) error { return renderRun(out, &run, nil) },
				"detail":  func(out *bytes.Buffer) error { return renderRunDetail(out, &run) },
			} {
				var out bytes.Buffer
				require.NoError(t, render(&out))
				assert.Contains(t, out.String(), failureReasonText, caller)
			}
			err := runCompleted(&run)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "finished with status failed")
			assert.Contains(t, err.Error(), failureReasonText,
				"the line a pipeline logs carries the reason, not only the status")
		})
	}
}

func TestRunWithoutAReasonStillSaysItFailed(t *testing.T) {
	for _, run := range []*eval_api.OpenAIEvalRun{
		{ID: "run_x", Status: "failed"},
		{ID: "run_x", Status: "failed", Error: &eval_api.JobError{}},
	} {
		err := runCompleted(run)
		require.Error(t, err)
		assert.Equal(t, "run run_x finished with status failed", err.Error())
	}
	assert.NoError(t, runCompleted(&eval_api.OpenAIEvalRun{ID: "run_x", Status: "completed"}))
}

func TestRunFailureReasonInTheErrorLineIsRedactedAndBounded(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID: "run_x", Status: "failed",
		Error: &eval_api.JobError{Message: runFailureWithCredentials + "\n" + strings.Repeat("x", 1000)},
	}
	err := runCompleted(run)
	require.Error(t, err)
	text := err.Error()
	for _, secret := range []string{"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment", "sig="} {
		assert.NotContains(t, text, secret)
	}
	assert.Contains(t, text, "Synthetic initialization failure.")
	assert.NotContains(t, text, "\n", "the error is one line")
	assert.LessOrEqual(t, len([]rune(text)), 400)
	assert.True(t, strings.HasSuffix(text, "..."), "a reason that was cut says so")
}

func rowErrorItem(id string, results ...eval_api.OutputResult) eval_api.OutputItem {
	return eval_api.OutputItem{ID: id, RunID: "run_rows", Status: "errored", Results: results}
}

func erroredResult(evaluator, code, message string) eval_api.OutputResult {
	return eval_api.OutputResult{
		Name: evaluator, Status: "errored",
		Sample: &eval_api.OutputSample{Error: &eval_api.SampleError{Code: code, Message: message}},
	}
}

func TestRowErrorsAreGroupedAndCounted(t *testing.T) {
	groups := summarizeRowErrors([]eval_api.OutputItem{
		rowErrorItem("1",
			erroredResult("quality", "RateLimit", "slow down"),
			erroredResult("quality", "RateLimit", "slow down"), // same row, same reason: one row
			erroredResult("safety", "", "no deployment")),
		rowErrorItem("2", erroredResult("quality", "RateLimit", "slow down")),
		rowErrorItem("3",
			eval_api.OutputResult{Name: "quality", Status: "errored"}, // errored without any reason
			erroredResult("quality", "", "")),                         // empty sample error is not a reason
		{ID: "4", Results: []eval_api.OutputResult{{Name: "quality", Label: "pass"}}},
	})
	require.Equal(t, []rowErrorGroup{
		{evaluator: "quality", code: "RateLimit", message: "slow down", rows: 2},
		{evaluator: "safety", message: "no deployment", rows: 1},
	}, groups)
}

func TestRowErrorsPrintTheActualMessage(t *testing.T) {
	var out bytes.Buffer
	renderRowErrors(&out, []rowErrorGroup{
		{evaluator: "quality", code: "RateLimit", message: "slow down", rows: 2},
		{evaluator: "safety", message: "no deployment", rows: 1},
		{code: "OnlyACode", rows: 1},
		{evaluator: "latency", message: "timed out", rows: 5},
		{evaluator: "tone", message: "never shown", rows: 1},
	})
	text := out.String()
	assert.Contains(t, text, "EVALUATOR ERRORS")
	assert.Contains(t, text, "quality: RateLimit: slow down (2 rows)")
	assert.Contains(t, text, "safety: no deployment (1 row)")
	assert.Contains(t, text, "evaluator: OnlyACode (1 row)", "a reason without an evaluator name is still printed")
	assert.NotContains(t, text, "never shown", "only the first reasons are printed")
	assert.Contains(t, text, "... and 2 more")

	var empty bytes.Buffer
	renderRowErrors(&empty, nil)
	assert.Empty(t, empty.String(), "a run with no row reasons prints no heading")
}

func TestRowErrorsAreRedactedAndOnOneLine(t *testing.T) {
	var out bytes.Buffer
	renderRowErrors(&out, []rowErrorGroup{{
		evaluator: "quality", code: "Failed", message: runFailureWithCredentials + "\n" + strings.Repeat("y", 500), rows: 1,
	}})
	text := out.String()
	for _, secret := range []string{"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment", "sig="} {
		assert.NotContains(t, text, secret)
	}
	assert.Contains(t, text, "Synthetic initialization failure.")
	assert.Equal(t, 3, strings.Count(text, "\n"), "a leading blank line, the heading, and one reason")
}

// `run start --wait` prints the summary a person reads. When every row errored
// the run-level error is usually empty and the reasons live on the rows, which
// is where the summary has to look.
func TestRunSummaryReadsReasonsFromErroredRows(t *testing.T) {
	const items = `{"data":[` +
		`{"id":"1","run_id":"run_rows","status":"errored","results":[` +
		`{"name":"quality","status":"errored","sample":{"error":{"code":"DeploymentNotFound","message":"` +
		failureReasonText + `"}}}]},` +
		`{"id":"2","run_id":"run_rows","status":"errored","results":[` +
		`{"name":"quality","status":"errored","sample":{"error":{"code":"DeploymentNotFound","message":"` +
		failureReasonText + `"}}}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/output_items") {
			_, _ = w.Write([]byte(items))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	for _, status := range []string{"failed", "completed"} {
		t.Run(status, func(t *testing.T) {
			var run eval_api.OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(`{"id":"run_rows","eval_id":"eval_rows","status":"`+status+`",`+
				`"error":null,"result_counts":{"total":2,"errored":2}}`), &run))
			ec := evalContextFor(srv)
			summary := ec.runOutputSummary(t.Context(), "eval_rows", &run)
			require.NotNil(t, summary)

			var out bytes.Buffer
			require.NoError(t, renderRun(&out, &run, summary))
			text := out.String()
			assert.Contains(t, text, "EVALUATOR ERRORS")
			assert.Contains(t, text, "quality: DeploymentNotFound: "+failureReasonText+" (2 rows)")

			var without bytes.Buffer
			require.NoError(t, renderRun(&without, &run, nil))
			assert.NotContains(t, without.String(), failureReasonText,
				"without the row listing there is nothing to read, and nothing is invented")
		})
	}
}

// -o json carries the service's own object, so a nested reason is data the
// caller already has, and reading it for the human view must not rewrite it.
func TestShowJSONLeavesANestedReasonUntouched(t *testing.T) {
	const response = `{"id":"run_failed","status":"failed","error":{"code":"E","message":"",` +
		`"details":[{"code":"d","message":"` + failureReasonText + `"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)

	var out bytes.Buffer
	command := jsonCmd(t, "json")
	command.SetContext(t.Context())
	command.SetOut(&out)
	action := &runShowAction{cmd: command, runID: "run_failed", flags: &runShowFlags{}}
	require.NoError(t, action.show(t.Context(), evalContextFor(srv), "eval_failed", gate{}))
	assert.JSONEq(t, response, out.String())
}

// Reasons are grouped by what a reader would see, so messages that differ only
// in a part redaction removes are one reason rather than a screenful of
// identical-looking lines.
func TestRowErrorsAreGroupedByWhatIsPrinted(t *testing.T) {
	var items []eval_api.OutputItem
	for _, signature := range []string{"aaa", "bbb", "ccc"} {
		items = append(items, rowErrorItem(signature, erroredResult(
			"quality", "Download", "could not read https://storage.example/rows.jsonl?sig="+signature)))
	}
	groups := summarizeRowErrors(items)
	require.Len(t, groups, 1)
	assert.Equal(t, 3, groups[0].rows)
	assert.NotContains(t, groups[0].message, "sig=")
}

// The cap on printed lines must not hide the reason most rows share.
func TestTheMostCommonRowErrorComesFirst(t *testing.T) {
	var items []eval_api.OutputItem
	for _, message := range []string{"rare one", "rare two", "rare three"} {
		items = append(items, rowErrorItem(message, erroredResult("quality", "", message)))
	}
	for range 5 {
		items = append(items, rowErrorItem("common", erroredResult("quality", "", "dominant")))
	}
	groups := summarizeRowErrors(items)
	require.Len(t, groups, 4)
	assert.Equal(t, "dominant", groups[0].message)
	assert.Equal(t, 5, groups[0].rows)
	assert.Equal(t, []string{"rare one", "rare two", "rare three"},
		[]string{groups[1].message, groups[2].message, groups[3].message}, "ties keep first-seen order")

	var out bytes.Buffer
	renderRowErrors(&out, groups)
	assert.Contains(t, out.String(), "dominant (5 rows)")
	assert.NotContains(t, out.String(), "rare three")
	assert.Contains(t, out.String(), "... and 1 more")
}

func TestJobFailureLineIsRedactedAndReadsNestedReasons(t *testing.T) {
	var job eval_api.GenerationJob
	require.NoError(t, json.Unmarshal([]byte(`{"id":"job_1","status":"failed","error":{"code":"x","message":"",`+
		`"details":[{"message":"could not read https://user:pw@storage.example/rows.jsonl?sig=secret-signature"}]}}`),
		&job))

	var out bytes.Buffer
	writeJobFailure(&out, &job)
	text := out.String()
	assert.True(t, strings.HasPrefix(text, "error: could not read "), text)
	for _, secret := range []string{"user:pw", "secret-signature", "sig="} {
		assert.NotContains(t, text, secret)
	}

	var none bytes.Buffer
	writeJobFailure(&none, &eval_api.GenerationJob{ID: "job_2", Status: "failed"})
	writeJobFailure(&none, nil)
	assert.Empty(t, none.String(), "a failure with no reason prints no error line")
}
