// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobErrorReadsTheReasonWhereverTheServiceSentIt(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		code, message string
		reason        string
	}{
		{"message", `{"code":"Init","message":"deployment not found"}`,
			"Init", "deployment not found", "deployment not found"},
		{"code only", `{"code":"Throttled"}`, "Throttled", "", ""},
		{"numeric code is left to the service JSON", `{"code":429,"message":"slow down"}`, "", "slow down", "slow down"},
		{"details array", `{"code":"x","message":"","details":[{"code":"d","message":"first"},{"message":"second"}]}`,
			"x", "", "first; second"},
		{"azure innererror", `{"code":"x","innererror":{"code":"i","message":"inner reason"}}`, "x", "", "inner reason"},
		{"snake case inner_error", `{"inner_error":{"message":"snake reason"}}`, "", "", "snake reason"},
		{"openai error wrapper", `{"message":"","error":{"message":"wrapped reason"}}`, "", "", "wrapped reason"},
		{"message wins over nested", `{"message":"top","innererror":{"message":"deep"}}`, "", "top", "top"},
		{"message is an object", `{"code":"c","message":{"value":"x","message":"object reason"}}`,
			"c", "object reason", "object reason"},
		{"bare string", `"it simply failed"`, "", "it simply failed", "it simply failed"},
		{"array of strings", `["one","","two"]`, "", "one; two", "one; two"},
		{"null", `null`, "", "", ""},
		{"empty object", `{}`, "", "", ""},
		{"boolean ignored", `true`, "", "", ""},
		{"whitespace only message", `{"message":"   ","details":[{"message":"real"}]}`, "", "   ", "real"},
		{"member names match case-insensitively", `{"Code":"E","Message":"pascal reason"}`,
			"E", "pascal reason", "pascal reason"},
		{"camel case innerError", `{"Code":"E","innerError":{"Message":"camel reason"}}`, "E", "", "camel reason"},
		{"upper case DETAILS", `{"DETAILS":[{"MESSAGE":"upper reason"}]}`, "", "", "upper reason"},
		{
			"too deep to follow",
			`{"innererror":{"innererror":{"innererror":{"innererror":{"innererror":{"message":"lost"}}}}}}`,
			"", "", "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded JobError
			require.NoError(t, json.Unmarshal([]byte(tc.body), &decoded))
			assert.Equal(t, tc.code, decoded.Code)
			assert.Equal(t, tc.message, decoded.Message)
			assert.Equal(t, tc.reason, decoded.Reason())
		})
	}
}

func TestJobErrorReasonIsNilSafe(t *testing.T) {
	var none *JobError
	assert.Empty(t, none.Reason())
	assert.Equal(t, "plain", (&JobError{Message: " plain "}).Reason())
}

// A run whose error does not have the documented shape must still be a run. It
// failed the whole response before, which hid the run behind a parse error.
func TestRunWithAnUnconventionalErrorStillDecodes(t *testing.T) {
	for name, errorBody := range map[string]string{
		"bare string":        `"the model deployment was not found"`,
		"message as object":  `{"message":{"message":"the model deployment was not found"}}`,
		"nested in details":  `{"message":"","details":[{"message":"the model deployment was not found"}]}`,
		"nested innererror":  `{"code":"E","innererror":{"message":"the model deployment was not found"}}`,
		"openai style error": `{"error":{"message":"the model deployment was not found"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var run OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(`{"id":"run_1","status":"failed","error":`+errorBody+`}`), &run))
			assert.Equal(t, "run_1", run.ID)
			assert.Equal(t, "the model deployment was not found", run.Failure())
		})
	}
}

// Nested detail is read, not rewritten: a service object that carried it is
// emitted back with its own members and no member the service did not send.
func TestRunJSONKeepsTheServicesErrorMembers(t *testing.T) {
	const body = `{"id":"run_1","status":"failed","error":{"code":"E","message":"",` +
		`"details":[{"code":"d","message":"reason"}],"future":9007199254740993}}`
	var run OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(body), &run))
	require.Equal(t, "reason", run.Failure())

	out, err := json.Marshal(&run)
	require.NoError(t, err)
	assert.JSONEq(t, body, string(out))
	assert.True(t, strings.Contains(string(out), "9007199254740993"), "numeric precision is preserved")
}

// An error in the conventional object shape keeps emitting exactly what the
// service sent, including text with surrounding spaces, member names in another
// case, and a numeric code (which is not re-typed), none of which are trimmed
// or rewritten on the way through.
func TestRunJSONDoesNotRewriteAConventionalError(t *testing.T) {
	for _, errorBody := range []string{
		`{"code":" E ","message":"  padded  "}`,
		`{"code":429,"message":"numeric code"}`,
		`{"Code":"E","Message":"Pascal case"}`,
		`{"code":"E","message":"","details":[{"message":"nested"}]}`,
	} {
		t.Run(errorBody, func(t *testing.T) {
			body := `{"id":"run_1","status":"failed","error":` + errorBody + `}`
			var run OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(body), &run))
			out, err := json.Marshal(&run)
			require.NoError(t, err)
			assert.JSONEq(t, body, string(out))
		})
	}
}

// Shapes that used to fail decoding outright are read for their text and are
// emitted as the conventional {code, message} object. Pinned so the change in
// what -o json shows for them is a decision rather than an accident.
func TestRunJSONNormalizesAnErrorThatCouldNotDecodeBefore(t *testing.T) {
	for name, tc := range map[string]struct{ errorBody, want string }{
		"bare string":       {`"it failed"`, `{"message":"it failed"}`},
		"array of strings":  {`["a","b"]`, `{"message":"a; b"}`},
		"message is object": {`{"code":"E","message":{"message":"obj"}}`, `{"code":"E","message":"obj"}`},
	} {
		t.Run(name, func(t *testing.T) {
			var run OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(`{"id":"run_1","status":"failed","error":`+tc.errorBody+`}`), &run))
			out, err := json.Marshal(&run)
			require.NoError(t, err)
			assert.JSONEq(t, `{"id":"run_1","status":"failed","error":`+tc.want+`}`, string(out))
		})
	}
}

func TestGenerationJobFailureNamesANestedReason(t *testing.T) {
	var job GenerationJob
	require.NoError(t, json.Unmarshal([]byte(
		`{"id":"j","status":"failed","error":{"code":"x","details":[{"message":"quota exhausted"}]}}`), &job))
	failure := &JobFailedError{Job: &job, Status: JobStatusFailed}
	assert.Contains(t, failure.Error(), "quota exhausted")

	empty := &JobFailedError{Job: &GenerationJob{Status: "failed"}, Status: JobStatusFailed}
	assert.Contains(t, empty.Error(), "failed", "a failure with no reason still says something")
	assert.NotContains(t, empty.Error(), ": ")
}

// A reason the service quotes can carry a credential in a URL; the polled-job
// error is returned to the terminal and CI logs as-is, so it is redacted here.
func TestGenerationJobFailureDoesNotDiscloseACredentialInTheReason(t *testing.T) {
	//nolint:gosec // Synthetic URL credentials verify non-disclosure; this fixture contains no real secret.
	for name, body := range map[string]string{
		"top level message": `{"message":"could not read https://acct.blob.core.windows.net/c/f.jsonl?sv=1&sig=SECRETSIG"}`,
		"nested detail":     `{"details":[{"message":"fetch https://user:hunter2@host.example/x failed"}]}`,
		"bare string":       `"see https://acct.blob.core.windows.net/c?sig=SECRETSIG"`,
	} {
		t.Run(name, func(t *testing.T) {
			var job GenerationJob
			require.NoError(t, json.Unmarshal([]byte(`{"id":"j","status":"failed","error":`+body+`}`), &job))
			text := (&JobFailedError{Job: &job, Status: JobStatusFailed}).Error()
			assert.NotContains(t, text, "SECRETSIG")
			assert.NotContains(t, text, "hunter2")
			assert.NotContains(t, text, "sig=")
		})
	}
}
