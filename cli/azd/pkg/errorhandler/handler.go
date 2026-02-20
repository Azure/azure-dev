// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import "context"

// ErrorHandler processes an error and returns a user-friendly suggestion.
// Handlers are registered by name in the IoC container and referenced
// from YAML rules via the "handler" field.
type ErrorHandler interface {
	// Handle inspects the error and returns a suggestion if applicable.
	// Returns nil if this handler cannot produce a suggestion for the given error.
	Handle(ctx context.Context, err error) *ErrorWithSuggestion
}
