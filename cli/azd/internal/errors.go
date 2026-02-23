// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import "github.com/azure/azure-dev/cli/azd/pkg/errorhandler"

// ErrorWithSuggestion is a type alias for errorhandler.ErrorWithSuggestion.
// The canonical type lives in pkg/errorhandler so it can be used by extensions.
type ErrorWithSuggestion = errorhandler.ErrorWithSuggestion

// ErrorWithTraceId is a custom error type that includes a trace ID for the current operation
type ErrorWithTraceId struct {
	TraceId string
	Err     error
}

// Error returns the error message
func (et *ErrorWithTraceId) Error() string {
	return et.Err.Error()
}

// Unwrap returns the wrapped error
func (et *ErrorWithTraceId) Unwrap() error {
	return et.Err
}
