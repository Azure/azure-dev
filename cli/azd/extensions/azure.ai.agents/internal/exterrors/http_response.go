// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type responseReadClassificationPolicy struct{}

// NewResponseReadClassificationPolicy returns a per-retry policy that
// tags post-2xx body read failures before auth policies drop resp.
func NewResponseReadClassificationPolicy() policy.Policy {
	return responseReadClassificationPolicy{}
}

func (responseReadClassificationPolicy) Do(req *policy.Request) (*http.Response, error) {
	resp, err := req.Next()
	if err != nil &&
		resp != nil &&
		resp.StatusCode >= http.StatusOK &&
		resp.StatusCode < http.StatusMultipleChoices &&
		!IsCancellation(err) &&
		!isDeadlineOrTimeout(err) {
		return resp, WrapResponseRead(err, "")
	}
	return resp, err
}

// WrapPipelineError wraps an azcore pipeline error, classifying
// post-2xx body read failures as [ResponseReadError].
func WrapPipelineError(resp *http.Response, err error, contextMessage string) error {
	if err == nil {
		return nil
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if existing, ok := errors.AsType[*ResponseReadError](err); ok && existing.Context == "" {
		return &ResponseReadError{
			Context: contextMessage,
			Err:     existing.Err,
		}
	}
	if resp != nil &&
		resp.StatusCode >= http.StatusOK &&
		resp.StatusCode < http.StatusMultipleChoices &&
		!IsCancellation(err) &&
		!isDeadlineOrTimeout(err) {
		return WrapResponseRead(err, contextMessage)
	}
	if contextMessage == "" {
		return err
	}
	return fmt.Errorf("%s: %w", contextMessage, err)
}

// ResponseReadError indicates that reading an HTTP response body failed after
// the service returned a success status code.
type ResponseReadError struct {
	Context string
	Err     error
}

// Error implements the error interface.
func (e *ResponseReadError) Error() string {
	if e == nil {
		return "failed to read response body"
	}
	msg := e.Context
	if msg == "" {
		msg = "failed to read response body"
	}
	if e.Err == nil {
		return msg
	}
	return fmt.Sprintf("%s: %s", msg, e.Err)
}

// Unwrap returns the underlying read error.
func (e *ResponseReadError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// WrapResponseRead wraps an HTTP response body read error so [ServiceFromAzure]
// can distinguish post-2xx stream read failures from pre-response transport errors.
func WrapResponseRead(err error, contextMessage string) error {
	if err == nil {
		return nil
	}
	return &ResponseReadError{
		Context: contextMessage,
		Err:     err,
	}
}

// ResponseParseError indicates that decoding or validating a structured HTTP
// response payload failed after the body was received.
type ResponseParseError struct {
	Context string
	Err     error
}

// Error implements the error interface.
func (e *ResponseParseError) Error() string {
	if e == nil {
		return "failed to parse response"
	}
	msg := e.Context
	if msg == "" {
		msg = "failed to parse response"
	}
	if e.Err == nil {
		return msg
	}
	return fmt.Sprintf("%s: %s", msg, e.Err)
}

// Unwrap returns the underlying parse error.
func (e *ResponseParseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// WrapResponseParse wraps an HTTP response payload decoding error so
// [ServiceFromAzure] can classify malformed 2xx payloads distinctly.
func WrapResponseParse(err error, contextMessage string) error {
	if err == nil {
		return nil
	}
	return &ResponseParseError{
		Context: contextMessage,
		Err:     err,
	}
}
