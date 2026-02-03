// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

// ErrorSuggestionRule defines a single rule that maps error patterns to an actionable suggestion.
type ErrorSuggestionRule struct {
	// Patterns is a list of strings to match against error messages.
	// Simple strings are matched as case-insensitive substrings.
	// Prefix a pattern with "regex:" to use regular expression matching.
	Patterns []string `yaml:"patterns"`

	// Message is a user-friendly error message that explains what went wrong.
	// This replaces the cryptic system error with something readable.
	Message string `yaml:"message"`

	// Suggestion is the actionable next steps for the user to resolve the issue.
	Suggestion string `yaml:"suggestion"`

	// DocUrl is an optional link to documentation for more information.
	DocUrl string `yaml:"docUrl,omitempty"`
}

// ErrorSuggestionsConfig is the root structure for the error_suggestions.yaml file.
type ErrorSuggestionsConfig struct {
	// Rules is the ordered list of error suggestion rules.
	// Rules are evaluated in order; the first match wins.
	Rules []ErrorSuggestionRule `yaml:"rules"`
}

// MatchedSuggestion represents a successful match of an error to a suggestion rule.
type MatchedSuggestion struct {
	// Message is a user-friendly error message.
	Message string

	// Suggestion is the actionable next steps for the user.
	Suggestion string

	// DocUrl is an optional documentation link.
	DocUrl string
}
