// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refusalFrom builds the error azcore hands back for a refused call.
func refusalFrom(t *testing.T, status int, rawURL, body string) *azcore.ResponseError {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return &azcore.ResponseError{
		StatusCode: status,
		ErrorCode:  "InvalidRequest",
		RawResponse: &http.Response{
			StatusCode: status,
			Request:    &http.Request{Method: http.MethodPost, URL: u},
			Body:       io.NopCloser(strings.NewReader(body)),
		},
	}
}

// A refusal says what the service said, not what the wire carried.
//
// azcore prints the request line, the status, the code and the entire response
// body between rules. Thirty lines of JSON around one sentence reached the user
// verbatim, and under `-o json` it is not even valid output.
func TestARefusalIsReducedToTheSentenceItCarried(t *testing.T) {
	body := `{"error":{"code":"InvalidRequest","message":"dataset 'golden' has no version 3.0"}}`
	got := ServiceRefused(400, refusalFrom(t, 400, "https://proj.services.ai.azure.com/datasets/golden", body))

	require.Error(t, got)
	text := got.Error()
	assert.Contains(t, text, "dataset 'golden' has no version 3.0",
		"the sentence is what the reader came for")
	assert.NotContains(t, text, `{"error"`,
		"and the envelope around it is a debugging artifact")
	assert.Contains(t, text, "proj.services.ai.azure.com",
		"the host stays: a command that reaches three services has to say which refused")
}

// The most specific complaint wins. The outer message is usually "the request
// is invalid", which is the one thing the reader already knows.
func TestTheInnermostMessageIsTheOneReported(t *testing.T) {
	body := `{"error":{"code":"BadRequest","message":"The request is invalid.",
		"innererror":{"code":"Schema","message":"column 'query' is required"}}}`
	got := ServiceRefused(400, refusalFrom(t, 400, "https://p.example/x", body))

	assert.Contains(t, got.Error(), "column 'query' is required")
	assert.NotContains(t, got.Error(), "The request is invalid.")
}

// A download refusal carries its SAS in the query, so the URL cannot be
// printed as given.
func TestARefusalNeverEchoesTheCredential(t *testing.T) {
	got := ServiceRefused(403, refusalFrom(t, 403,
		"https://acct.blob.core.windows.net/c/rows.jsonl?sig=SECRETSIGNATURE&se=2026-01-01",
		`{"error":{"message":"Server failed to authenticate the request."}}`))

	text := got.Error()
	assert.NotContains(t, text, "SECRETSIGNATURE")
	assert.NotContains(t, text, "sig=")
	assert.Contains(t, text, "acct.blob.core.windows.net/c/rows.jsonl",
		"the path stays, because it names what was refused")
}

// The headline is shaped like every detail: a service that puts a credential-
// bearing URL, a line break or a very long body in its message does not get any of
// them printed, in the human line or in the sentence `-o json` reads.
func TestTheRefusalHeadlineIsRedactedOneLineAndBounded(t *testing.T) {
	body := `{"error":{"message":"Cannot read https://acct.blob.core.windows.net/c/rows.jsonl?sig=SECRETSIGNATURE\n` +
		`second line ` + strings.Repeat("x", 600) + `"}}`
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized} {
		got := ServiceRefused(status, refusalFrom(t, status, "https://p.example/x", body))

		text := got.Error()
		assert.NotContains(t, text, "SECRETSIGNATURE", "status %d", status)
		assert.NotContains(t, text, "\n", "status %d", status)
		assert.Contains(t, text, "Cannot read", "status %d", status)
		assert.Less(t, len([]rune(text)), 500, "status %d", status)

		var safe interface{ SafeMessage() string }
		if errors.As(got, &safe) {
			assert.NotContains(t, safe.SafeMessage(), "SECRETSIGNATURE", "status %d", status)
			assert.NotContains(t, safe.SafeMessage(), "\n", "status %d", status)
		}
	}
}

// A 401 or 403 carries the same details as any other refusal: they name which
// field the service objected to, and they travel through the same path, so the
// credential in the URL stays out of them too.
func TestARefusedAuthCallNamesItsDetailsWithoutTheCredential(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		body := `{"error":{"message":"Access denied.","details":[` +
			`{"message":"role 'Contributor' is required",` +
			`"target":"https://acct.blob.core.windows.net/c/rows.jsonl?sig=SECRETSIGNATURE"}]}}`
		got := ServiceRefused(status, refusalFrom(t, status,
			"https://acct.blob.core.windows.net/c/rows.jsonl?sig=SECRETSIGNATURE", body))

		text := got.Error()
		assert.Contains(t, text, "role 'Contributor' is required", "status %d", status)
		assert.NotContains(t, text, "SECRETSIGNATURE", "status %d", status)
	}
}

// The status checks read the chain, not the text. Replacing the azcore error
// rather than wrapping it made a 404 stop reading as absence, and an unknown
// dataset became a failed command.
func TestAConciseRefusalStillAnswersTheStatusChecks(t *testing.T) {
	original := refusalFrom(t, 404, "https://p.example/x", `{"error":{"message":"not found"}}`)
	got := ServiceRefused(404, original)

	var found *azcore.ResponseError
	require.ErrorAs(t, got, &found, "the response has to stay reachable underneath")
	assert.Equal(t, 404, found.StatusCode)
}

// A body that is not the shape we expect is not a reason to say nothing: the
// status and the code are still worth reporting.
func TestARefusalWithNoReadableMessageStillReportsTheStatus(t *testing.T) {
	got := ServiceRefused(500, refusalFrom(t, 500, "https://p.example/x", "<html>gateway</html>"))
	assert.Contains(t, got.Error(), "500")
	assert.Contains(t, got.Error(), "InvalidRequest")
	assert.NotContains(t, got.Error(), "<html>")
}

// Anything that is not a service refusal passes through untouched.
func TestANonServiceErrorIsLeftAlone(t *testing.T) {
	err := assertAnError{}
	assert.Equal(t, err, conciseServiceError(err))
}

type assertAnError struct{}

func (assertAnError) Error() string { return "a local problem" }

// The live shape of a create refused for a judge model the project does not
// have: the message only says the request was invalid, and the one place that
// names the model, with the part of the request it is about, is details[0].
const missingJudgeBody = `{"error":{"code":"invalid_request","message":"The request is invalid.",
	"details":[{"code":"model_not_found",
	"message":"Model 'missing-judge' was not found. Verify the name and version are correct.",
	"target":"testing_criteria[0].initialization_parameters.model"}]}}`

func TestARefusalNamesWhatTheDetailsSay(t *testing.T) {
	got := ServiceRefused(400, refusalFrom(t, 400, "https://proj.example/openai/v1/evals", missingJudgeBody))

	require.Error(t, got)
	text := got.Error()
	assert.Contains(t, text, "The request is invalid.")
	assert.Contains(t, text, "Model 'missing-judge' was not found. Verify the name and version are correct.")
	assert.Contains(t, text, "(target: testing_criteria[0].initialization_parameters.model)")
	assert.NotContains(t, text, "\n", "one line")

	svc, ok := errors.AsType[*serviceError](got)
	require.True(t, ok)
	assert.NotContains(t, svc.SafeMessage(), "missing-judge",
		"-o json is unchanged: the safe sentence is the one it always was")
	assert.Equal(t, "The request is invalid. (HTTP 400 InvalidRequest)", svc.SafeMessage())
}

func TestRefusalDetailsAreDeduplicatedOrderedCappedAndCounted(t *testing.T) {
	var details []string
	details = append(details, `{"message":"the request is invalid"}`) // restates the sentence
	for i := range 12 {
		details = append(details, fmt.Sprintf(`{"message":"problem %d"}`, i))
	}
	details = append(details, `{"message":"problem 0"}`) // a repeat
	body := `{"error":{"message":"The request is invalid.","details":[` + strings.Join(details, ",") + `]}}`

	text := ServiceRefused(400, refusalFrom(t, 400, "https://p.example/x", body)).Error()
	assert.Contains(t, text, "(details: problem 0; problem 1; problem 2; and 9 more)")
	assert.NotContains(t, text, "the request is invalid;")
}

func TestRefusalDetailsAreRedactedAndOnOneLine(t *testing.T) {
	body := `{"error":{"message":"Bad.","details":[{"message":"could not read ` +
		`https://fixture-user:fixture-password@storage.example/rows.jsonl?sig=fixture-signature\nsecond line ` +
		strings.Repeat("x", 600) + `","target":"https://storage.example/rows.jsonl?sig=fixture-signature"}]}}`

	text := ServiceRefused(400, refusalFrom(t, 400, "https://p.example/x", body)).Error()
	for _, secret := range []string{"fixture-user", "fixture-password", "fixture-signature", "sig="} {
		assert.NotContains(t, text, secret)
	}
	assert.NotContains(t, text, "\n")
	assert.Contains(t, text, "...", "a detail that was cut says so")
	assert.Less(t, len([]rune(text)), 900, "bounded")
}

func TestRefusalDetailsReadBareStringsAndNestedLevels(t *testing.T) {
	body := `{"error":{"message":"Bad.","innererror":{"details":["inner one",{"code":"OnlyACode"}]},` +
		`"details":["outer one"]}}`
	text := ServiceRefused(400, refusalFrom(t, 400, "https://p.example/x", body)).Error()
	assert.Contains(t, text, "(details: outer one; inner one; OnlyACode)")
}

func TestARefusalWithoutDetailsReadsAsItAlwaysDid(t *testing.T) {
	body := `{"error":{"code":"InvalidRequest","message":"dataset 'golden' has no version 3.0"}}`
	text := ServiceRefused(400, refusalFrom(t, 400, "https://proj.example/datasets/golden", body)).Error()
	assert.Equal(t,
		"dataset 'golden' has no version 3.0 (HTTP 400 InvalidRequest) from https://proj.example/datasets/golden", text)
}

// Repeats are dropped before the read cap applies, so sixty restatements of the
// headline cannot hide the one entry that names the cause, nor inflate the count.
func TestARunOfRepeatedDetailsDoesNotHideTheCause(t *testing.T) {
	var repeats []string
	for range 60 {
		repeats = append(repeats, `{"message":"The request is invalid."}`)
	}
	body := `{"error":{"message":"The request is invalid.","details":[` + strings.Join(repeats, ",") +
		`,{"message":"Model 'gpt-9' was not found.","target":"model"}]}}`
	got := ServiceRefused(400, refusalFrom(t, 400, "https://p.example/x", body))

	text := got.Error()
	assert.Contains(t, text, "Model 'gpt-9' was not found. (target: model)")
	assert.NotContains(t, text, "more", "no repeat is counted as a further finding")
}
