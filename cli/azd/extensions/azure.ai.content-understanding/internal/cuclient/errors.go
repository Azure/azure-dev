// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"context"
	"time"
)

type ErrorKind string

const (
	ErrorValidation     ErrorKind = "validation"
	ErrorNotFound       ErrorKind = "not_found"
	ErrorConflict       ErrorKind = "conflict"
	ErrorAuthentication ErrorKind = "authentication"
	ErrorThrottled      ErrorKind = "throttled"
	ErrorLimit          ErrorKind = "limit"
	ErrorCancelled      ErrorKind = "cancelled"
	ErrorTimeout        ErrorKind = "timeout"
	ErrorService        ErrorKind = "service"
)

// Error contains only safe user-facing diagnostics. Cause is retained for
// errors.Is/errors.As, but is deliberately excluded from Error's message.
type Error struct {
	Kind       ErrorKind
	Code       string
	Message    string
	Suggestion string
	StatusCode int
	RetryAfter time.Duration
	Details    []Diagnostic
	Cause      error
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (e *Error) Is(target error) bool {
	switch target {
	case context.Canceled:
		return e.Kind == ErrorCancelled
	case context.DeadlineExceeded:
		return e.Kind == ErrorTimeout
	default:
		return false
	}
}

func validationError(code, message string) error {
	return &Error{
		Kind: ErrorValidation, Code: code, Message: message,
		Suggestion: "Correct the request values and retry.",
	}
}
