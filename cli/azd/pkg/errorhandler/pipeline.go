// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"context"
	"log"
	"sync"

	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/braydonk/yaml"
)

// HandlerResolver resolves named ErrorHandler instances.
// Typically backed by the IoC container.
type HandlerResolver func(name string) (ErrorHandler, error)

// ErrorHandlerPipeline evaluates error suggestion rules from YAML
// and optionally invokes named ErrorHandlers for dynamic suggestions.
type ErrorHandlerPipeline struct {
	rules           []ErrorSuggestionRule
	matcher         *PatternMatcher
	handlerResolver HandlerResolver
}

var (
	pipelineConfig     *ErrorSuggestionsConfig
	pipelineConfigOnce sync.Once
)

func loadPipelineConfig() *ErrorSuggestionsConfig {
	pipelineConfigOnce.Do(func() {
		pipelineConfig = &ErrorSuggestionsConfig{}
		if err := yaml.Unmarshal(resources.ErrorSuggestions, pipelineConfig); err != nil {
			log.Panicf("failed to unmarshal error_suggestions.yaml: %v", err)
		}
	})
	return pipelineConfig
}

// NewErrorHandlerPipeline creates a new pipeline with rules loaded from the embedded YAML.
func NewErrorHandlerPipeline(handlerResolver HandlerResolver) *ErrorHandlerPipeline {
	cfg := loadPipelineConfig()
	return &ErrorHandlerPipeline{
		rules:           cfg.Rules,
		matcher:         NewPatternMatcher(),
		handlerResolver: handlerResolver,
	}
}

// Process evaluates all rules in order against the given error.
// Returns the first matching suggestion, or nil if no rules match.
//
// Rule evaluation:
//  1. If errorType is set → find matching typed error via reflection
//  2. If properties is set → check property values on the matched error
//  3. If patterns is set → check text patterns against error message
//  4. All specified conditions must pass for a match
//  5. If handler is set → invoke named handler for dynamic suggestion
//  6. Otherwise → return static suggestion from rule fields
func (p *ErrorHandlerPipeline) Process(ctx context.Context, err error) *ErrorWithSuggestion {
	return p.processRules(ctx, err, p.rules)
}

// ProcessWithRules evaluates the given rules against the error.
// This is useful for testing with custom rule sets.
func (p *ErrorHandlerPipeline) ProcessWithRules(
	ctx context.Context,
	err error,
	rules []ErrorSuggestionRule,
) *ErrorWithSuggestion {
	return p.processRules(ctx, err, rules)
}

func (p *ErrorHandlerPipeline) processRules(
	ctx context.Context,
	err error,
	rules []ErrorSuggestionRule,
) *ErrorWithSuggestion {
	errMessage := err.Error()

	for i := range rules {
		rule := &rules[i]
		suggestion := p.evaluateRule(ctx, err, errMessage, rule)
		if suggestion != nil {
			return suggestion
		}
	}

	return nil
}

func (p *ErrorHandlerPipeline) evaluateRule(
	ctx context.Context,
	err error,
	errMessage string,
	rule *ErrorSuggestionRule,
) *ErrorWithSuggestion {
	// Track whether any condition is specified
	hasCondition := false

	// 1. Check errorType via reflection (and properties together)
	if rule.ErrorType != "" {
		hasCondition = true
		_, ok := findErrorByTypeName(
			err, rule.ErrorType, rule.Properties, p.matcher, rule.Regex,
		)
		if !ok {
			return nil
		}
	} else if len(rule.Properties) > 0 {
		// Properties without errorType is invalid — skip
		return nil
	}

	// 3. Check text patterns
	if len(rule.Patterns) > 0 {
		hasCondition = true
		if !p.matcher.Match(errMessage, rule.Patterns, rule.Regex) {
			return nil
		}
	}

	// No conditions specified → skip rule
	if !hasCondition {
		return nil
	}

	// All conditions passed — produce suggestion
	// 4. If handler is set, invoke it
	if rule.Handler != "" {
		return p.invokeHandler(ctx, err, *rule)
	}

	// 5. Return static suggestion
	links := make([]ErrorLink, len(rule.Links))
	for i, l := range rule.Links {
		links[i] = ErrorLink(l)
	}

	return &ErrorWithSuggestion{
		Err:        err,
		Message:    rule.Message,
		Suggestion: rule.Suggestion,
		Links:      links,
	}
}

func (p *ErrorHandlerPipeline) invokeHandler(
	ctx context.Context, err error, rule ErrorSuggestionRule,
) *ErrorWithSuggestion {
	if p.handlerResolver == nil {
		return nil
	}

	handler, resolveErr := p.handlerResolver(rule.Handler)
	if resolveErr != nil || handler == nil {
		return nil
	}

	return handler.Handle(ctx, err, rule)
}
