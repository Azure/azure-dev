// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"regexp"
	"strings"
)

const regexPrefix = "regex:"

// PatternMatcher handles matching error messages against patterns.
type PatternMatcher struct {
	// compiledPatterns caches compiled regex patterns for performance
	compiledPatterns map[string]*regexp.Regexp
}

// NewPatternMatcher creates a new PatternMatcher instance.
func NewPatternMatcher() *PatternMatcher {
	return &PatternMatcher{
		compiledPatterns: make(map[string]*regexp.Regexp),
	}
}

// Match checks if the given error message matches any of the patterns.
// Returns true if any pattern matches.
//
// Pattern types:
//   - Simple string: case-insensitive substring match
//   - "regex:pattern": regular expression match
func (m *PatternMatcher) Match(errorMessage string, patterns []string) bool {
	lowerErrorMessage := strings.ToLower(errorMessage)

	for _, pattern := range patterns {
		if m.matchPattern(errorMessage, lowerErrorMessage, pattern) {
			return true
		}
	}

	return false
}

// matchPattern checks if a single pattern matches the error message.
func (m *PatternMatcher) matchPattern(errorMessage, lowerErrorMessage, pattern string) bool {
	if strings.HasPrefix(pattern, regexPrefix) {
		return m.matchRegex(errorMessage, pattern[len(regexPrefix):])
	}

	// Simple case-insensitive substring match
	return strings.Contains(lowerErrorMessage, strings.ToLower(pattern))
}

// matchRegex compiles (with caching) and matches a regex pattern against the error message.
func (m *PatternMatcher) matchRegex(errorMessage, pattern string) bool {
	re, ok := m.compiledPatterns[pattern]
	if !ok {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			// Invalid regex pattern - skip this pattern
			return false
		}
		m.compiledPatterns[pattern] = re
	}

	return re.MatchString(errorMessage)
}
