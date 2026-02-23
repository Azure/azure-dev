// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import "context"

// ErrorHandler processes an error and returns a user-friendly suggestion.
// Handlers are registered by name in the IoC container and referenced
// from YAML rules via the "handler" field.
type ErrorHandler interface {
	// Handle inspects the error and returns a suggestion if applicable.
	// The rule parameter provides access to the matching YAML rule,
	// allowing the handler to merge in links or other static data.
	// Returns nil if this handler cannot produce a suggestion.
	Handle(ctx context.Context, err error, rule ErrorSuggestionRule) *ErrorWithSuggestion
}
