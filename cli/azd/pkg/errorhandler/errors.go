// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

// ErrorWithSuggestion is a custom error type that includes user-friendly messaging.
// It wraps an original error with a human-readable message, actionable suggestion,
// and optional documentation link.
type ErrorWithSuggestion struct {
	// Err is the original underlying error
	Err error
	// Message is a user-friendly explanation of what went wrong
	Message string
	// Suggestion is actionable next steps to resolve the issue
	Suggestion string
	// DocUrl is an optional link to documentation
	DocUrl string
}

// Error returns the error message
func (es *ErrorWithSuggestion) Error() string {
	return es.Err.Error()
}

// Unwrap returns the wrapped error
func (es *ErrorWithSuggestion) Unwrap() error {
	return es.Err
}
