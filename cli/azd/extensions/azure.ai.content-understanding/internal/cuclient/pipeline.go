// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const maxErrorBodyBytes = 64 * 1024

var serviceCodePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func newHTTPTransport(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Bound error responses before azcore's downloader and error policies buffer them.
type boundedErrorTransport struct {
	inner policy.Transporter
}

func (t *boundedErrorTransport) Do(request *http.Request) (*http.Response, error) {
	response, err := t.inner.Do(request)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, &Error{Kind: ErrorService, Code: "missing_response", Message: "The HTTP transport returned no response."}
	}
	if response.Body == nil {
		response.Body = http.NoBody
	}
	if response.StatusCode >= 300 && response.Body != nil {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			return nil, transportError(errors.Join(readErr, closeErr))
		}
		if len(body) > maxErrorBodyBytes {
			body = nil
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		response.ContentLength = int64(len(body))
	}
	return response, nil
}

func serviceResponseError(statusCode int, code, retryAfterHeader string) error {
	if !serviceCodePattern.MatchString(code) {
		code = fmt.Sprintf("http_%d", statusCode)
	}
	kind := ErrorService
	suggestion := "Check the service configuration and retry."
	switch statusCode {
	case http.StatusBadRequest:
		kind = ErrorValidation
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = ErrorAuthentication
		suggestion = "Check the azd account, selected tenant, and access to the CU resource."
	case http.StatusNotFound:
		kind = ErrorNotFound
	case http.StatusConflict:
		kind = ErrorConflict
	case http.StatusRequestEntityTooLarge:
		kind = ErrorLimit
	case http.StatusTooManyRequests:
		kind = ErrorThrottled
		suggestion = "Wait for the service retry interval before trying again."
	}
	return &Error{
		Kind: kind, Code: code, StatusCode: statusCode,
		Message:    fmt.Sprintf("The Content Understanding request failed (HTTP %d).", statusCode),
		Suggestion: suggestion, RetryAfter: retryAfter(retryAfterHeader, time.Now()),
	}
}

func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(min(seconds, 86400)) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		return max(0, min(date.Sub(now), 24*time.Hour))
	}
	return 0
}

func transportError(err error) error {
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Kind: ErrorCancelled, Code: "canceled", Message: "The request was cancelled.", Cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: ErrorTimeout, Code: "deadline_exceeded", Message: "The request timed out.", Cause: err}
	}
	return &Error{
		Kind: ErrorService, Code: "transport_failed", Message: "The CU service request could not be completed.",
		Suggestion: "Check connectivity and retry.", Cause: err,
	}
}
