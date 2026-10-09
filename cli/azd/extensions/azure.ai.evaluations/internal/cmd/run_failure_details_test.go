// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The error a run returns when it names a model the project does not have: the
// top-level message only says that validation failed, and the one place the
// missing model is named is the detail, with the request member it is about.
const (
	liveTopMessage    = "Evaluation validation failed: model resource is not found."
	liveDetailMessage = "Model 'missing-model' was not found. Verify the name and version are correct."
	liveDetailTarget  = "run.data_source.model_configuration.model"
	liveErrorBody     = `{"code":"validation_failed","message":"` + liveTopMessage + `","details":[` +
		`{"code":"model_not_found","message":"` + liveDetailMessage + `","target":"` + liveDetailTarget + `"}]}`
	liveRunBody = `{"id":"run_live","eval_id":"eval_live","status":"failed","error":` + liveErrorBody + `}`
)

func assertNamesTheMissingModel(t *testing.T, text string) {
	t.Helper()
	assert.Contains(t, text, liveTopMessage)
	assert.Contains(t, text, liveDetailMessage, "the detail names the model; nothing else does")
	assert.Contains(t, text, liveDetailTarget)
}

func TestFailedRunViewsNameTheMissingModelFromTheDetails(t *testing.T) {
	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(liveRunBody), &run))

	var summary bytes.Buffer
	require.NoError(t, renderRun(&summary, &run, nil))
	assertNamesTheMissingModel(t, summary.String())

	var detail bytes.Buffer
	require.NoError(t, renderRunDetail(&detail, &run))
	assertNamesTheMissingModel(t, detail.String())

	err := runCompleted(&run)
	require.Error(t, err)
	assert.Equal(t, "run run_live finished with status failed: "+liveTopMessage+
		" (details: "+liveDetailMessage+" (target: "+liveDetailTarget+"))", err.Error(),
		"the one line a pipeline logs carries the detail too")
	assert.NotContains(t, err.Error(), "\n")
}

func liveRunServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/openai/v1/evals/eval_live":
			_, _ = io.WriteString(w, `{"id":"eval_live","data_source_config":{"type":"custom"}}`)
		case strings.HasSuffix(r.URL.Path, "/output_items"):
			_, _ = io.WriteString(w, `{"data":[]}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = io.WriteString(w, `{"id":"run_live","status":"queued"}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/runs/run_live"):
			_, _ = io.WriteString(w, body)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = io.WriteString(w, `{"data":[{"id":"previous","data_source":{"type":"jsonl"}}]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunShowNamesTheMissingModelAndJSONIsTheServicesOwn(t *testing.T) {
	srv := liveRunServer(t, liveRunBody)

	var out bytes.Buffer
	command := jsonCmd(t, "table")
	command.SetContext(t.Context())
	command.SetOut(&out)
	action := &runShowAction{cmd: command, runID: "run_live", flags: &runShowFlags{}}
	require.NoError(t, action.show(t.Context(), evalContextFor(srv), "eval_live", gate{}))
	assertNamesTheMissingModel(t, out.String())

	var machine bytes.Buffer
	asJSON := jsonCmd(t, "json")
	asJSON.SetContext(t.Context())
	asJSON.SetOut(&machine)
	showJSON := &runShowAction{cmd: asJSON, runID: "run_live", flags: &runShowFlags{}}
	require.NoError(t, showJSON.show(t.Context(), evalContextFor(srv), "eval_live", gate{}))
	assert.JSONEq(t, liveRunBody, machine.String(), "-o json is the service's object, unchanged")
}

func TestRunStartWaitNamesTheMissingModel(t *testing.T) {
	srv := liveRunServer(t, liveRunBody)

	var out bytes.Buffer
	command := jsonCmd(t, "table")
	command.SetContext(t.Context())
	command.SetOut(&out)
	action := &runStartAction{cmd: command, flags: &runStartFlags{
		groupName: "eval_live", evalPath: t.TempDir(), wait: true,
	}}
	err := action.start(t.Context(), evalContextFor(srv), gate{})
	require.ErrorContains(t, err, "run run_live finished with status failed: "+liveTopMessage)
	require.ErrorContains(t, err, liveDetailMessage)
	assertNamesTheMissingModel(t, out.String())
}

func TestJobFailureNamesTheDetailsUnderItsMessage(t *testing.T) {
	var job eval_api.GenerationJob
	require.NoError(t, json.Unmarshal([]byte(`{"id":"job_1","status":"failed","error":`+liveErrorBody+`}`), &job))

	var out bytes.Buffer
	writeJobFailure(&out, &job)
	assert.Equal(t, "error: "+liveTopMessage+"\n  - "+liveDetailMessage+" (target: "+liveDetailTarget+")\n", out.String())
}

func failureWithDetails(t *testing.T, body string) *eval_api.JobError {
	t.Helper()
	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"id":"r","status":"failed","error":`+body+`}`), &run))
	require.NotNil(t, run.Error)
	return run.Error
}

func TestFailureDetailsAreDeduplicatedOrderedAndCounted(t *testing.T) {
	failure := failureWithDetails(t, `{"message":"Evaluation validation failed.","details":[`+
		`{"message":"evaluation validation failed"},`+ // restates the headline
		`{"message":"first"},`+
		`{"message":"first"},`+ // an exact repeat
		`{"message":"  second  ","target":"a.b"},`+
		`{"code":"OnlyACode"},`+ // no message: the code is what there is
		`{},`+ // nothing to say
		`"a bare string",`+
		`{"message":"fourth"},{"message":"fifth"},{"message":"sixth"}]}`)

	lines, more := failureDetails(failure, maxFailureDetails)
	assert.Equal(t, []string{"first", "second (target: a.b)", "OnlyACode", "a bare string", "fourth"}, lines)
	assert.Equal(t, 2, more)

	var out bytes.Buffer
	renderFailureDetails(&out, failure)
	assert.Equal(t, "  - first\n  - second (target: a.b)\n  - OnlyACode\n  - a bare string\n  - fourth\n"+
		"  ... and 2 more not shown\n", out.String())

	inline, hidden := failureDetails(failure, maxFailureDetailsInline)
	assert.Equal(t, []string{"first", "second (target: a.b)", "OnlyACode"}, inline)
	assert.Equal(t, 4, hidden)
}

func TestDetailsAreNotRepeatedWhenTheyAreTheHeadline(t *testing.T) {
	// With no message of its own the first nested reason is already the headline,
	// so there is nothing further to list and the existing view is unchanged.
	failure := failureWithDetails(t, `{"code":"E","message":"","details":[{"message":"the only reason"}]}`)
	lines, more := failureDetails(failure, maxFailureDetails)
	assert.Empty(t, lines)
	assert.Zero(t, more)

	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"id":"r","status":"failed","error":`+
		`{"message":"","details":[{"message":"the only reason"}]}}`), &run))
	var out bytes.Buffer
	renderRunFailure(&out, &run)
	assert.Equal(t, "\nthe only reason\n", out.String())
	assert.Equal(t, "run r finished with status failed: the only reason", runCompleted(&run).Error())
}

func TestFailureWithoutDetailsPrintsExactlyWhatItDidBefore(t *testing.T) {
	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"id":"r","status":"failed","error":`+
		`{"code":"E","message":"the model was not found"}}`), &run))
	var out bytes.Buffer
	renderRunFailure(&out, &run)
	assert.Equal(t, "\nthe model was not found\n", out.String())
	assert.Equal(t, "run r finished with status failed: the model was not found", runCompleted(&run).Error())

	var job bytes.Buffer
	writeJobFailure(&job, &eval_api.GenerationJob{ID: "j", Status: "failed", Error: run.Error})
	assert.Equal(t, "error: the model was not found\n", job.String())
}

// A detail is the service's words about the request, so it can quote a URL the
// way the headline can: it is redacted, collapsed to one line and bounded, in
// the message and in the target, on every surface.
var sensitiveFixtureParts = []string{
	"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment", "sig=",
}

func TestFailureDetailsAreRedactedAndBounded(t *testing.T) {
	failure := failureWithDetails(t, `{"message":"Evaluation validation failed.","details":[`+
		`{"message":"could not read `+runFailureWithCredentials+`\n`+strings.Repeat("z", 1000)+`",`+
		`"target":"https://fixture-user:fixture-password@storage.example/rows.jsonl`+
		`?sig=fixture-signature#fixture-fragment"}]}`)

	var run eval_api.OpenAIEvalRun
	run.ID, run.Status, run.Error = "run_redact", "failed", failure
	var rendered bytes.Buffer
	renderRunFailure(&rendered, &run)
	var job bytes.Buffer
	writeJobFailure(&job, &eval_api.GenerationJob{ID: "j", Status: "failed", Error: failure})
	line := runCompleted(&run).Error()

	for name, text := range map[string]string{"view": rendered.String(), "job": job.String(), "line": line} {
		for _, secret := range sensitiveFixtureParts {
			assert.NotContains(t, text, secret, name)
		}
		assert.Contains(t, text, "Synthetic initialization failure.", name)
	}
	assert.Equal(t, 1, strings.Count(line, "\n")+1, "the error is one line")
	assert.LessOrEqual(t, len([]rune(line)), 1000, "a long detail is cut, and the line stays bounded")
	assert.Contains(t, line, "...", "a detail that was cut says so")
}

// A detail is a repeat only when it reads the same as the headline or an earlier
// detail. One that merely contains, or is contained in, another still says
// something, and one that names a different target is about a different thing.
func TestFailureDetailsKeepWhatIsNotAnExactRepeat(t *testing.T) {
	failure := failureWithDetails(t, `{"message":"Model not found for the run.","details":[`+
		`{"message":"gpt-4o-mini"},{"message":"gpt-4o"},`+ // one inside the other
		`{"message":"not found","target":"run.model"},`+ // inside the headline, but names a target
		`{"message":"missing","target":"a"},{"message":"missing","target":"b"},`+ // same words, two targets
		`{"message":"missing","target":"a"},`+ // the very same line again
		`{"message":"Not Found."}]}`) // inside the headline: nothing more to say

	lines, more := failureDetails(failure, maxFailureDetails)
	assert.Equal(t, []string{
		"gpt-4o-mini", "gpt-4o", "not found (target: run.model)", "missing (target: a)", "missing (target: b)",
	}, lines)
	assert.Zero(t, more)
}

func TestFailureDetailTargetsAreBounded(t *testing.T) {
	failure := failureWithDetails(t, `{"message":"m","details":[{"message":"d","target":"`+strings.Repeat("t", 2000)+`"}]}`)
	lines, _ := failureDetails(failure, maxFailureDetails)
	require.Len(t, lines, 1)
	assert.LessOrEqual(t, len([]rune(lines[0])), 2*maxFailureTextRunes+len(" (target: ...)"))
	assert.Contains(t, lines[0], "...", "a target that was cut says so")
}

// A service reason is data: it can carry newlines or a body of any size, and the
// headline of a failed run is printed from it like every other reason is.
func TestTheRunFailureHeadlineIsOneBoundedLine(t *testing.T) {
	var run eval_api.OpenAIEvalRun
	body := `{"id":"r","status":"failed","error":{"message":"first line\nsecond line\n` + strings.Repeat("x", 1000) + `"}}`
	require.NoError(t, json.Unmarshal([]byte(body), &run))

	var out bytes.Buffer
	renderRunFailure(&out, &run)
	text := strings.TrimPrefix(out.String(), "\n")
	assert.Equal(t, 1, strings.Count(text, "\n"), "one line, then the newline that ends it")
	assert.Contains(t, text, "first line second line xxx")
	assert.LessOrEqual(t, len([]rune(strings.TrimSpace(text))), maxFailureTextRunes+3)
	assert.True(t, strings.HasSuffix(strings.TrimSpace(text), "..."), "a reason that was cut says so")
}

// -o json keeps the service's own spelling of a key, so a diagnostic written as
// `Message` is the one that is redacted, not left beside a redacted `message`.
func TestJSONRedactsADiagnosticWhateverCaseTheServiceSpelledItIn(t *testing.T) {
	const secret = "fixture-signature"
	for _, key := range []string{"message", "Message", "MESSAGE"} {
		t.Run(key, func(t *testing.T) {
			raw := `{"id":"run_1","status":"failed","error":{"code":"E","` + key + `":"could not read ` +
				`https://storage.example/rows.jsonl?sig=` + secret + `"}}`
			var run eval_api.OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(raw), &run))

			projected, err := runForJSON(&run)
			require.NoError(t, err)
			out, err := json.Marshal(projected)
			require.NoError(t, err)
			assert.NotContains(t, string(out), secret)
			var decoded struct {
				Error map[string]any `json:"error"`
			}
			require.NoError(t, json.Unmarshal(out, &decoded))
			assert.Contains(t, decoded.Error, key, "the service's spelling is kept")
			assert.Len(t, decoded.Error, 2, "and nothing is added beside it: code and the message")

			exported, err := redactExportRunError(json.RawMessage(raw))
			require.NoError(t, err)
			assert.NotContains(t, string(exported), secret)
			assert.Contains(t, string(exported), `"`+key+`"`)
		})
	}
}

func TestDetailsBeyondTheStoredCapAreStillCounted(t *testing.T) {
	var details []string
	for i := range 60 {
		details = append(details, fmt.Sprintf(`{"message":"detail %d"}`, i))
	}
	failure := failureWithDetails(t,
		`{"message":"Evaluation validation failed.","details":[`+strings.Join(details, ",")+`]}`)

	lines, more := failureDetails(failure, maxFailureDetails)
	assert.Len(t, lines, maxFailureDetails)
	assert.Equal(t, 60-maxFailureDetails, more, "every entry that is not shown is counted, stored or not")
	assert.Equal(t, 10, failure.OmittedDetails(), "ten entries were beyond the fifty kept")

	inline, hidden := failureDetails(failure, maxFailureDetailsInline)
	assert.Len(t, inline, maxFailureDetailsInline)
	assert.Equal(t, 60-maxFailureDetailsInline, hidden)
}

func TestAJobFailureReasonPrintsAsOneBoundedLine(t *testing.T) {
	var job eval_api.GenerationJob
	require.NoError(t, json.Unmarshal([]byte(
		`{"id":"j","status":"failed","error":{"message":"first line\nsecond line `+
			`\u001b[2J\u0007\u009b31m`+strings.Repeat("x", 600)+`"}}`), &job))

	var out bytes.Buffer
	writeJobFailure(&out, &job)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	assert.Len(t, lines, 1)
	assert.Contains(t, out.String(), "first line second line")
	for _, control := range []string{"\x1b", "\x07", "\u009b"} {
		assert.NotContains(t, out.String(), control)
	}
	assert.Less(t, len([]rune(out.String())), 400)
}

func TestFailureDetailsWithoutATopLevelMessageCountRemainingCauses(t *testing.T) {
	var failure eval_api.JobError
	require.NoError(t, json.Unmarshal([]byte(`{"details":[
		{"message":"first cause"},{"message":"second cause"},{"message":"third cause"}]}`), &failure))

	lines, more := failureDetails(&failure, 1)
	assert.Equal(t, []string{"second cause"}, lines)
	assert.Equal(t, 1, more)
}
