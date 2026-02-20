// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

// ErrorSuggestionRule defines a single rule that maps error patterns to an actionable suggestion.
type ErrorSuggestionRule struct {
	// Patterns is a list of strings to match against error messages.
	// By default, strings are matched as case-insensitive substrings.
	// Set Regex to true to treat all patterns and property values as regular expressions.
	Patterns []string `yaml:"patterns,omitempty"`

	// ErrorType is the Go struct type name to match via reflection.
	// The error chain is walked using errors.As semantics to find a matching type.
	// Example: "AzureDeploymentError", "AuthFailedError"
	ErrorType string `yaml:"errorType,omitempty"`

	// Properties is a map of dot-path property names to expected values.
	// Properties are resolved via reflection on the matched error type.
	// By default, values are matched as case-insensitive substrings.
	// Set Regex to true to treat values as regular expressions.
	// Example: {"Details.Code": "FlagMustBeSetForRestore"}
	Properties map[string]string `yaml:"properties,omitempty"`

	// Regex enables regular expression matching for all patterns and property values
	// in this rule. When false (default), patterns and property values use
	// case-insensitive substring matching.
	Regex bool `yaml:"regex,omitempty"`

	// Handler is the name of a registered ErrorHandler to invoke when this rule matches.
	// The handler is resolved from the IoC container by name.
	// When set, the handler computes the suggestion dynamically instead of using
	// the static message/suggestion/docUrl fields.
	Handler string `yaml:"handler,omitempty"`

	// Message is a user-friendly error message that explains what went wrong.
	// This replaces the cryptic system error with something readable.
	Message string `yaml:"message,omitempty"`

	// Suggestion is the actionable next steps for the user to resolve the issue.
	Suggestion string `yaml:"suggestion,omitempty"`

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
