// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
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
