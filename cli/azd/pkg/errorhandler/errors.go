// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

// ErrorLink represents a reference link with a URL and optional title.
type ErrorLink struct {
	// URL is the link target (required)
	URL string
	// Title is the display text (optional — if empty, the URL is shown)
	Title string
}

// ErrorWithSuggestion is a custom error type that includes user-friendly messaging.
// It wraps an original error with a human-readable message, actionable suggestion,
// and optional reference links.
type ErrorWithSuggestion struct {
	// Err is the original underlying error
	Err error
	// Message is a user-friendly explanation of what went wrong
	Message string
	// Suggestion is actionable next steps to resolve the issue
	Suggestion string
	// Links is an optional list of reference links
	Links []ErrorLink
}

// Error returns the error message
func (es *ErrorWithSuggestion) Error() string {
	return es.Err.Error()
}

// Unwrap returns the wrapped error
func (es *ErrorWithSuggestion) Unwrap() error {
	return es.Err
}
