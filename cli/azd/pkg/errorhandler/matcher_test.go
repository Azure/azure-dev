// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPatternMatcher_SimpleSubstring(t *testing.T) {
	matcher := NewPatternMatcher()

	tests := []struct {
		name         string
		errorMessage string
		patterns     []string
		expected     bool
	}{
		{
			name:         "exact match",
			errorMessage: "quota exceeded",
			patterns:     []string{"quota exceeded"},
			expected:     true,
		},
		{
			name:         "case insensitive match",
			errorMessage: "QUOTA EXCEEDED",
			patterns:     []string{"quota exceeded"},
			expected:     true,
		},
		{
			name:         "substring match",
			errorMessage: "Error: quota exceeded for subscription",
			patterns:     []string{"quota exceeded"},
			expected:     true,
		},
		{
			name:         "no match",
			errorMessage: "some other error",
			patterns:     []string{"quota exceeded"},
			expected:     false,
		},
		{
			name:         "multiple patterns first matches",
			errorMessage: "QuotaExceeded error",
			patterns:     []string{"QuotaExceeded", "quota exceeded"},
			expected:     true,
		},
		{
			name:         "multiple patterns second matches",
			errorMessage: "quota exceeded error",
			patterns:     []string{"QuotaExceeded", "quota exceeded"},
			expected:     true,
		},
		{
			name:         "empty patterns",
			errorMessage: "some error",
			patterns:     []string{},
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matcher.Match(tt.errorMessage, tt.patterns)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPatternMatcher_Regex(t *testing.T) {
	matcher := NewPatternMatcher()

	tests := []struct {
		name         string
		errorMessage string
		patterns     []string
		expected     bool
	}{
		{
			name:         "regex match",
			errorMessage: "Authorization failed for user",
			patterns:     []string{"regex:(?i)authorization.*failed"},
			expected:     true,
		},
		{
			name:         "regex case insensitive flag",
			errorMessage: "AUTHORIZATION FAILED",
			patterns:     []string{"regex:(?i)authorization.*failed"},
			expected:     true,
		},
		{
			name:         "regex no match",
			errorMessage: "some other error",
			patterns:     []string{"regex:(?i)authorization.*failed"},
			expected:     false,
		},
		{
			name:         "regex with numbers",
			errorMessage: "Error BCP123: invalid syntax",
			patterns:     []string{"regex:BCP\\d{3}"},
			expected:     true,
		},
		{
			name:         "invalid regex is skipped",
			errorMessage: "some error",
			patterns:     []string{"regex:[invalid"},
			expected:     false,
		},
		{
			name:         "mixed patterns regex first",
			errorMessage: "quota limit reached",
			patterns:     []string{"regex:(?i)quota.*limit", "QuotaExceeded"},
			expected:     true,
		},
		{
			name:         "mixed patterns simple first",
			errorMessage: "QuotaExceeded",
			patterns:     []string{"QuotaExceeded", "regex:(?i)quota.*limit"},
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matcher.Match(tt.errorMessage, tt.patterns)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPatternMatcher_RegexCaching(t *testing.T) {
	matcher := NewPatternMatcher()
	pattern := "regex:test\\d+"

	// First call compiles the regex
	result1 := matcher.Match("test123", []string{pattern})
	assert.True(t, result1)

	// Second call should use cached regex
	result2 := matcher.Match("test456", []string{pattern})
	assert.True(t, result2)

	// Verify cache has the entry
	assert.Len(t, matcher.compiledPatterns, 1)
}

func TestErrorSuggestionService_FindSuggestion(t *testing.T) {
	service := NewErrorSuggestionService()

	tests := []struct {
		name           string
		errorMessage   string
		expectMatch    bool
		expectDocUrl   bool
		expectMessage  bool
		suggestionPart string
	}{
		{
			name:           "quota error matches",
			errorMessage:   "Deployment failed: QuotaExceeded for resource",
			expectMatch:    true,
			expectDocUrl:   true,
			expectMessage:  true,
			suggestionPart: "quota",
		},
		{
			name:           "auth error matches",
			errorMessage:   "AADSTS50076: authentication required",
			expectMatch:    true,
			expectDocUrl:   true,
			expectMessage:  true,
			suggestionPart: "azd auth login",
		},
		{
			name:           "bicep error matches",
			errorMessage:   "BCP035: The specified value is not valid",
			expectMatch:    true,
			expectDocUrl:   true,
			expectMessage:  true,
			suggestionPart: ".bicep",
		},
		{
			name:         "unknown error no match",
			errorMessage: "some completely unknown error xyz123",
			expectMatch:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.FindSuggestion(tt.errorMessage)
			if tt.expectMatch {
				assert.NotNil(t, result)
				assert.Contains(t, result.Suggestion, tt.suggestionPart)
				if tt.expectDocUrl {
					assert.NotEmpty(t, result.DocUrl)
				}
				if tt.expectMessage {
					assert.NotEmpty(t, result.Message)
				}
			} else {
				assert.Nil(t, result)
			}
		})
	}
}

func TestErrorSuggestionService_FirstMatchWins(t *testing.T) {
	service := NewErrorSuggestionService()

	// An error that could match multiple patterns should return the first match
	// "quota exceeded" and "OperationNotAllowed" are in the same rule
	result := service.FindSuggestion("OperationNotAllowed: quota exceeded")

	assert.NotNil(t, result)
	assert.Contains(t, result.Suggestion, "quota")
}
