// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"azureaieval/internal/failuretext"

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

	message, details, omitted := serviceFailureFrom(respErr)
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
	// The sentence often only says that the request was invalid; the details
	// name what in it, such as the model that does not exist. They are for the
	// human line only: safe, which -o json reads, stays the sentence it was.
	text += serviceDetailsSuffix(message, details, omitted)
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

// maxServiceDetails bounds how many detail messages follow a refusal's sentence
// on its one line.
const maxServiceDetails = 3

// maxServiceDetailsRead bounds how many entries of a details array are read.
const maxServiceDetailsRead = 50

// serviceDetailsSuffix is the detail messages that follow a refusal's sentence,
// shaped by failuretext like every other failure's: redacted, one line,
// bounded, deduplicated against the sentence, and counted when they do not all
// fit.
func serviceDetailsSuffix(message string, details []failuretext.Detail, omitted int) string {
	lines, more := failuretext.Lines(message, details, maxServiceDetails)
	if len(lines) == 0 {
		return ""
	}
	list := strings.Join(lines, "; ")
	if more += omitted; more > 0 {
		list += fmt.Sprintf("; and %d more", more)
	}
	return " (details: " + list + ")"
}

// serviceFailureFrom reads the sentence and the details out of an error response
// body.
func serviceFailureFrom(respErr *azcore.ResponseError) (message string, details []failuretext.Detail, omitted int) {
	envelope := serviceEnvelopeFrom(respErr)
	if envelope == nil {
		return "", nil, 0
	}
	omittedCount := 0
	collectServiceDetails(envelope, 0, &details, &omittedCount, map[string]bool{})
	return failuretext.Text(deepestMessage(envelope, 0)), details, omittedCount
}

// collectServiceDetails gathers the details arrays at every level the sentence
// can be nested at, outermost first, in the order the service listed them.
// Repeats are dropped before the read cap applies, so a run of identical entries
// cannot fill the room and hide the one that names the cause.
func collectServiceDetails(
	envelope map[string]json.RawMessage, depth int, out *[]failuretext.Detail, omitted *int, seen map[string]bool,
) {
	if depth > 8 {
		return
	}
	if raw, ok := envelope["details"]; ok {
		var entries []json.RawMessage
		if json.Unmarshal(raw, &entries) == nil {
			for _, entry := range entries {
				detail, ok := serviceDetailFrom(entry)
				if !ok {
					continue
				}
				if key := failuretext.Key(detail); key != "" {
					if seen[key] {
						continue
					}
					seen[key] = true
				}
				if len(*out) == maxServiceDetailsRead {
					*omitted++
					continue
				}
				*out = append(*out, detail)
			}
		}
	}
	for _, key := range []string{"error", "innererror", "innerError"} {
		var nested map[string]json.RawMessage
		if raw, ok := envelope[key]; ok && json.Unmarshal(raw, &nested) == nil {
			collectServiceDetails(nested, depth+1, out, omitted, seen)
		}
	}
}

// serviceDetailFrom reads one entry of a details array: a bare string is its
// message, an object is its code, message and target.
func serviceDetailFrom(raw json.RawMessage) (failuretext.Detail, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return failuretext.Detail{Message: text}, strings.TrimSpace(text) != ""
	}
	var entry struct {
		Code    json.RawMessage `json:"code"`
		Message json.RawMessage `json:"message"`
		Target  json.RawMessage `json:"target"`
	}
	if json.Unmarshal(raw, &entry) != nil {
		return failuretext.Detail{}, false
	}
	asText := func(value json.RawMessage) string {
		var s string
		if json.Unmarshal(value, &s) == nil {
			return strings.TrimSpace(s)
		}
		return ""
	}
	detail := failuretext.Detail{Code: asText(entry.Code), Message: asText(entry.Message), Target: asText(entry.Target)}
	return detail, detail.Code != "" || detail.Message != ""
}

// serviceEnvelopeFrom reads the error response body once.
func serviceEnvelopeFrom(respErr *azcore.ResponseError) map[string]json.RawMessage {
	if respErr.RawResponse == nil || respErr.RawResponse.Body == nil {
		return nil
	}
	// Bounded: this is a diagnostic, and a service that answers an error with
	// megabytes is not owed the memory to hold them.
	body, err := io.ReadAll(io.LimitReader(respErr.RawResponse.Body, 1<<20))
	if err != nil || len(body) == 0 {
		return nil
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	return envelope
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
