// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"log"
	"sync"

	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/braydonk/yaml"
)

var (
	config     *ErrorSuggestionsConfig
	configOnce sync.Once
)

// loadConfig loads the error suggestions configuration from the embedded YAML file.
func loadConfig() *ErrorSuggestionsConfig {
	configOnce.Do(func() {
		config = &ErrorSuggestionsConfig{}
		if err := yaml.Unmarshal(resources.ErrorSuggestions, config); err != nil {
			log.Panicf("failed to unmarshal error_suggestions.yaml: %v", err)
		}
	})
	return config
}

// ErrorSuggestionService provides error message matching against known error patterns.
type ErrorSuggestionService struct {
	config  *ErrorSuggestionsConfig
	matcher *PatternMatcher
}

// NewErrorSuggestionService creates a new ErrorSuggestionService.
func NewErrorSuggestionService() *ErrorSuggestionService {
	return &ErrorSuggestionService{
		config:  loadConfig(),
		matcher: NewPatternMatcher(),
	}
}

// FindSuggestion checks if the error message matches any known error patterns.
// Returns a MatchedSuggestion if a match is found, or nil if no match.
// Rules are evaluated in order; the first match wins.
func (s *ErrorSuggestionService) FindSuggestion(errorMessage string) *MatchedSuggestion {
	for _, rule := range s.config.Rules {
		if s.matcher.Match(errorMessage, rule.Patterns) {
			return &MatchedSuggestion{
				Message:    rule.Message,
				Suggestion: rule.Suggestion,
				DocUrl:     rule.DocUrl,
			}
		}
	}
	return nil
}
