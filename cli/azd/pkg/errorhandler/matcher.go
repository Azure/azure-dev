// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"regexp"
	"strings"
	"sync"
)

// PatternMatcher handles matching error messages against patterns.
type PatternMatcher struct {
	// compiledPatterns caches compiled regex patterns for performance
	mu               sync.RWMutex
	compiledPatterns map[string]*regexp.Regexp
}

// NewPatternMatcher creates a new PatternMatcher instance.
func NewPatternMatcher() *PatternMatcher {
	return &PatternMatcher{
		compiledPatterns: make(map[string]*regexp.Regexp),
	}
}

// Match checks if the given error message matches any of the patterns (OR logic).
//
// When useRegex is false, patterns are matched as case-insensitive substrings.
// When useRegex is true, patterns are treated as regular expressions.
func (m *PatternMatcher) Match(errorMessage string, patterns []string, useRegex bool) bool {
	lowerErrorMessage := strings.ToLower(errorMessage)

	for _, pattern := range patterns {
		if useRegex {
			if m.matchRegex(errorMessage, pattern) {
				return true
			}
		} else if strings.Contains(lowerErrorMessage, strings.ToLower(pattern)) {
			return true
		}
	}

	return false
}

// MatchSingle checks if a single value matches a pattern.
//
// When useRegex is false, the match is a case-insensitive substring check.
// When useRegex is true, the pattern is treated as a regular expression.
func (m *PatternMatcher) MatchSingle(value string, pattern string, useRegex bool) bool {
	if useRegex {
		return m.matchRegex(value, pattern)
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(pattern))
}

// matchRegex compiles (with caching) and matches a regex pattern against the text.
func (m *PatternMatcher) matchRegex(text, pattern string) bool {
	m.mu.RLock()
	re, ok := m.compiledPatterns[pattern]
	m.mu.RUnlock()

	if !ok {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			// Invalid regex pattern - skip
			return false
		}
		m.mu.Lock()
		m.compiledPatterns[pattern] = re
		m.mu.Unlock()
	}

	return re.MatchString(text)
}
