// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// conciseServiceError reduces a service refusal to the sentence it carried.
//
// azcore's ResponseError prints the request line, the status, the error code
// and the entire response body between rules -- thirty lines of JSON around one
// sentence. That is a debugging artifact, and it reached the user verbatim,
// including under `-o json`, where it is not even valid output.
//
// The original is kept underneath rather than discarded: IsNotFound, IsConflict
// and every other status check reach it through Unwrap, and replacing it with a
// plain error made a 404 stop reading as absence.
func conciseServiceError(err error) error {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok {
		return err
	}

	message := serviceMessageFrom(respErr)
	code := strings.TrimSpace(respErr.ErrorCode)
	var sentence string
	switch {
	case message != "" && code != "":
		sentence = fmt.Sprintf("%s (HTTP %d %s)", message, respErr.StatusCode, code)
	case message != "":
		sentence = fmt.Sprintf("%s (HTTP %d)", message, respErr.StatusCode)
	case code != "":
		sentence = fmt.Sprintf("the service refused the request: HTTP %d %s",
			respErr.StatusCode, code)
	default:
		sentence = fmt.Sprintf("the service refused the request: HTTP %d", respErr.StatusCode)
	}
	// text is the full diagnostic sentence Error() and existing human output
	// read; safe is the same sentence without the service endpoint, which is
	// what -o json reads instead so a refused request's JSON document never
	// discloses which Foundry account or project backed the call.
	text := sentence
	if target := refusedTarget(respErr); target != "" {
		text += " from " + target
	}
	stableCode := code
	if stableCode == "" {
		stableCode = fmt.Sprintf("http_%d", respErr.StatusCode)
	}
	return &serviceError{text: text, safe: sentence, code: stableCode, cause: err}
}

// refusedTarget names which service refused, and nothing else about the call.
//
// Scheme, host and path only: a download request carries its SAS in the query,
// so the URL cannot be printed as given. Without the host a caller who reaches
// three services in one command cannot tell which of them answered.
func refusedTarget(respErr *azcore.ResponseError) string {
	if respErr.RawResponse == nil || respErr.RawResponse.Request == nil {
		return ""
	}
	u := respErr.RawResponse.Request.URL
	if u == nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// serviceError says the sentence and carries the response underneath.
//
// text is the full diagnostic sentence, including which service refused the
// call; Error() and existing human output read it. safe is the same sentence
// without that service endpoint, read instead when serializing to -o json.
type serviceError struct {
	text  string
	safe  string
	code  string
	cause error
}

func (e *serviceError) Error() string { return e.text }

// Unwrap is what keeps the status checks working: they look for the azcore
// error by type, and it is still in the chain.
func (e *serviceError) Unwrap() error { return e.cause }

// SafeMessage is read instead of Error() when serializing to -o json, so the
// JSON document never discloses the full internal service endpoint.
func (e *serviceError) SafeMessage() string { return e.safe }

// Code is read alongside SafeMessage so a refused request's JSON document
// carries something stable to branch on, as every other validation failure
// already does.
func (e *serviceError) Code() string { return e.code }

// authServiceError pairs an auth-classified LocalError with a safe message
// that omits the service endpoint, mirroring serviceError's JSON/human split
// for the 401/403 range ServiceRefused reclassifies. Without this, the
// endpoint-bearing concise sentence ServiceRefused formats into the
// LocalError's own Message would reach -o json without redaction, since
// LocalError does not otherwise satisfy safeJSONError.
type authServiceError struct {
	*azdext.LocalError
	safe string
}

// Unwrap keeps azdext.ErrorSuggestion and other LocalError lookups working.
func (e *authServiceError) Unwrap() error { return e.LocalError }

// SafeMessage is read instead of Error() when serializing to -o json.
func (e *authServiceError) SafeMessage() string { return e.safe }

// Code satisfies the same JSON-safety contract as serviceError.Code; the
// embedded LocalError already carries the classification exterrors.Auth
// assigned.
func (e *authServiceError) Code() string { return e.LocalError.Code }

// serviceMessageFrom digs the human sentence out of an error response body.
//
// Azure wraps it as {"error":{"message":...}}, sometimes nested another level
// under innererror, and sometimes sends the sentence at the root. Reading only
// the outermost shape produced an empty message for exactly the responses worth
// reading, so all of them are tried.
func serviceMessageFrom(respErr *azcore.ResponseError) string {
	if respErr.RawResponse == nil || respErr.RawResponse.Body == nil {
		return ""
	}
	// Bounded: this is a diagnostic, and a service that answers an error with
	// megabytes is not owed the memory to hold them.
	body, err := io.ReadAll(io.LimitReader(respErr.RawResponse.Body, 1<<20))
	if err != nil || len(body) == 0 {
		return ""
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	return deepestMessage(envelope, 0)
}

// deepestMessage returns the most specific message the envelope carries.
//
// The inner one is the specific complaint and the outer one is usually "the
// request is invalid", so the innermost wins.
func deepestMessage(envelope map[string]json.RawMessage, depth int) string {
	// Bounded because the shape is the service's, not ours, and a document that
	// nests into itself would otherwise not terminate.
	if depth > 8 {
		return ""
	}
	found := ""
	if raw, ok := envelope["message"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			found = strings.TrimSpace(s)
		}
	}
	for _, key := range []string{"error", "innererror", "innerError"} {
		raw, ok := envelope[key]
		if !ok {
			continue
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err != nil {
			continue
		}
		if deeper := deepestMessage(nested, depth+1); deeper != "" {
			found = deeper
		}
	}
	return found
}
