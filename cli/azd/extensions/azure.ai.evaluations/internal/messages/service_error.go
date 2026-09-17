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
	var text string
	switch {
	case message != "" && code != "":
		text = fmt.Sprintf("%s (HTTP %d %s)", message, respErr.StatusCode, code)
	case message != "":
		text = fmt.Sprintf("%s (HTTP %d)", message, respErr.StatusCode)
	case code != "":
		text = fmt.Sprintf("the service refused the request: HTTP %d %s",
			respErr.StatusCode, code)
	default:
		text = fmt.Sprintf("the service refused the request: HTTP %d", respErr.StatusCode)
	}
	if target := refusedTarget(respErr); target != "" {
		text += " from " + target
	}
	return &serviceError{text: text, cause: err}
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
type serviceError struct {
	text  string
	cause error
}

func (e *serviceError) Error() string { return e.text }

// Unwrap is what keeps the status checks working: they look for the azcore
// error by type, and it is still in the chain.
func (e *serviceError) Unwrap() error { return e.cause }

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
