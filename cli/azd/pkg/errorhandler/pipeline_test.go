// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipeline_PatternMatching(t *testing.T) {
	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				Patterns:   []string{"quota exceeded"},
				Message:    "Quota limit reached.",
				Suggestion: "Request a quota increase.",
			},
		},
		matcher: NewPatternMatcher(),
	}

	result := pipeline.Process(context.Background(), errors.New("deployment failed: quota exceeded"))
	require.NotNil(t, result)
	assert.Equal(t, "Quota limit reached.", result.Message)
	assert.Equal(t, "Request a quota increase.", result.Suggestion)
}

func TestPipeline_ErrorTypeMatching(t *testing.T) {
	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				ErrorType:  "testDeploymentError",
				Message:    "Deployment failed.",
				Suggestion: "Check your template.",
			},
		},
		matcher: NewPatternMatcher(),
	}

	err := &testDeploymentError{Title: "test"}
	result := pipeline.Process(context.Background(), err)
	require.NotNil(t, result)
	assert.Equal(t, "Deployment failed.", result.Message)
}

func TestPipeline_ErrorTypeWithProperties(t *testing.T) {
	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				ErrorType:  "testDeploymentError",
				Properties: map[string]string{"Details.Code": "InsufficientQuota"},
				Message:    "Quota insufficient.",
				Suggestion: "Request increase.",
			},
			{
				ErrorType:  "testDeploymentError",
				Properties: map[string]string{"Details.Code": "AuthorizationFailed"},
				Message:    "Auth failed.",
				Suggestion: "Check permissions.",
			},
		},
		matcher: NewPatternMatcher(),
	}

	// Should match quota rule
	err1 := &testDeploymentError{
		Details: &testErrorDetails{Code: "InsufficientQuota"},
	}
	result1 := pipeline.Process(context.Background(), err1)
	require.NotNil(t, result1)
	assert.Equal(t, "Quota insufficient.", result1.Message)

	// Should match auth rule
	err2 := &testDeploymentError{
		Details: &testErrorDetails{Code: "AuthorizationFailed"},
	}
	result2 := pipeline.Process(context.Background(), err2)
	require.NotNil(t, result2)
	assert.Equal(t, "Auth failed.", result2.Message)

	// Should not match (different code)
	err3 := &testDeploymentError{
		Details: &testErrorDetails{Code: "SomethingElse"},
	}
	result3 := pipeline.Process(context.Background(), err3)
	assert.Nil(t, result3)
}

func TestPipeline_ErrorTypeWithPatterns(t *testing.T) {
	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				ErrorType:  "testDeploymentError",
				Patterns:   []string{"soft delete"},
				Message:    "Soft-deleted resource conflict.",
				Suggestion: "Purge the resource.",
			},
		},
		matcher: NewPatternMatcher(),
	}

	// Matches: correct type AND message contains pattern
	err1 := &testDeploymentError{Title: "soft delete conflict"}
	result1 := pipeline.Process(context.Background(), err1)
	require.NotNil(t, result1)
	assert.Equal(t, "Soft-deleted resource conflict.", result1.Message)

	// No match: correct type but wrong message
	err2 := &testDeploymentError{Title: "quota issue"}
	result2 := pipeline.Process(context.Background(), err2)
	assert.Nil(t, result2)

	// No match: wrong type
	result3 := pipeline.Process(context.Background(), errors.New("soft delete error"))
	assert.Nil(t, result3)
}

func TestPipeline_WrappedErrorType(t *testing.T) {
	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				ErrorType:  "testAuthError",
				Properties: map[string]string{"ErrorCode": "AUTH001"},
				Message:    "Auth error.",
				Suggestion: "Re-authenticate.",
			},
		},
		matcher: NewPatternMatcher(),
	}

	// Wrapped in another error
	innerErr := &testAuthError{ErrorCode: "AUTH001"}
	wrappedErr := &testWrappedError{msg: "outer error", inner: innerErr}

	result := pipeline.Process(context.Background(), wrappedErr)
	require.NotNil(t, result)
	assert.Equal(t, "Auth error.", result.Message)
}

func TestPipeline_Handler(t *testing.T) {
	handlerCalled := false
	mockHandler := &mockErrorHandler{
		handleFunc: func(_ context.Context, err error) *ErrorWithSuggestion {
			handlerCalled = true
			return &ErrorWithSuggestion{
				Err:        err,
				Message:    "Dynamic message",
				Suggestion: "Dynamic suggestion",
			}
		},
	}

	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				ErrorType: "testDeploymentError",
				Handler:   "testHandler",
			},
		},
		matcher: NewPatternMatcher(),
		handlerResolver: func(name string) (ErrorHandler, error) {
			if name == "testHandler" {
				return mockHandler, nil
			}
			return nil, fmt.Errorf("handler not found: %s", name)
		},
	}

	err := &testDeploymentError{Title: "test"}
	result := pipeline.Process(context.Background(), err)
	require.NotNil(t, result)
	assert.True(t, handlerCalled)
	assert.Equal(t, "Dynamic message", result.Message)
	assert.Equal(t, "Dynamic suggestion", result.Suggestion)
}

func TestPipeline_HandlerNotFound(t *testing.T) {
	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				ErrorType: "testDeploymentError",
				Handler:   "nonExistentHandler",
			},
		},
		matcher: NewPatternMatcher(),
		handlerResolver: func(name string) (ErrorHandler, error) {
			return nil, fmt.Errorf("not found: %s", name)
		},
	}

	err := &testDeploymentError{Title: "test"}
	result := pipeline.Process(context.Background(), err)
	// Handler not found → no suggestion, moves to next rule
	assert.Nil(t, result)
}

func TestPipeline_FirstMatchWins(t *testing.T) {
	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				Patterns:   []string{"error"},
				Message:    "First match",
				Suggestion: "First",
			},
			{
				Patterns:   []string{"error"},
				Message:    "Second match",
				Suggestion: "Second",
			},
		},
		matcher: NewPatternMatcher(),
	}

	result := pipeline.Process(context.Background(), errors.New("some error"))
	require.NotNil(t, result)
	assert.Equal(t, "First match", result.Message)
}

func TestPipeline_NoMatch(t *testing.T) {
	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				Patterns:   []string{"specific pattern"},
				Message:    "Match",
				Suggestion: "Do something",
			},
		},
		matcher: NewPatternMatcher(),
	}

	result := pipeline.Process(context.Background(), errors.New("completely unrelated error"))
	assert.Nil(t, result)
}

func TestPipeline_NoConditionsSkipped(t *testing.T) {
	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				// Rule with no conditions — should be skipped
				Message:    "Should not match",
				Suggestion: "No conditions",
			},
		},
		matcher: NewPatternMatcher(),
	}

	result := pipeline.Process(context.Background(), errors.New("any error"))
	assert.Nil(t, result)
}

func TestPipeline_PropertiesWithoutErrorTypeSkipped(t *testing.T) {
	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				// Properties without errorType — invalid, should be skipped
				Properties: map[string]string{"Code": "test"},
				Message:    "Should not match",
				Suggestion: "Invalid rule",
			},
		},
		matcher: NewPatternMatcher(),
	}

	result := pipeline.Process(context.Background(), errors.New("any error"))
	assert.Nil(t, result)
}

func TestPipeline_HandlerWithConditions(t *testing.T) {
	handlerCalled := false
	mockHandler := &mockErrorHandler{
		handleFunc: func(_ context.Context, err error) *ErrorWithSuggestion {
			handlerCalled = true
			return &ErrorWithSuggestion{
				Err:        err,
				Message:    "Handled",
				Suggestion: "Done",
			}
		},
	}

	pipeline := &ErrorHandlerPipeline{
		rules: []ErrorSuggestionRule{
			{
				ErrorType:  "testDeploymentError",
				Properties: map[string]string{"Details.Code": "SkuNotAvailable"},
				Handler:    "skuHandler",
			},
		},
		matcher: NewPatternMatcher(),
		handlerResolver: func(name string) (ErrorHandler, error) {
			return mockHandler, nil
		},
	}

	// Matching error type and properties → handler invoked
	err1 := &testDeploymentError{
		Details: &testErrorDetails{Code: "SkuNotAvailable"},
	}
	result1 := pipeline.Process(context.Background(), err1)
	require.NotNil(t, result1)
	assert.True(t, handlerCalled)

	// Wrong property value → handler NOT invoked
	handlerCalled = false
	err2 := &testDeploymentError{
		Details: &testErrorDetails{Code: "OtherCode"},
	}
	result2 := pipeline.Process(context.Background(), err2)
	assert.Nil(t, result2)
	assert.False(t, handlerCalled)
}

// mockErrorHandler is a test helper for ErrorHandler
type mockErrorHandler struct {
	handleFunc func(ctx context.Context, err error) *ErrorWithSuggestion
}

func (m *mockErrorHandler) Handle(
	ctx context.Context, err error, rule ErrorSuggestionRule,
) *ErrorWithSuggestion {
	return m.handleFunc(ctx, err)
}

// mockResponseError mimics azcore.ResponseError for testing typed error matching.
type mockResponseError struct {
	ErrorCode  string
	StatusCode int
}

func (e *mockResponseError) Error() string {
	return fmt.Sprintf("RESPONSE %d: %s", e.StatusCode, e.ErrorCode)
}

func TestPipeline_ResponseError_MatchesByErrorCode(t *testing.T) {
	respErr := &mockResponseError{
		ErrorCode:  "LocationNotAvailableForResourceType",
		StatusCode: 400,
	}
	wrappedErr := fmt.Errorf("validating deployment: %w", respErr)

	pipeline := NewErrorHandlerPipeline(nil)
	result := pipeline.ProcessWithRules(
		context.Background(),
		wrappedErr,
		[]ErrorSuggestionRule{
			{
				ErrorType: "mockResponseError",
				Properties: map[string]string{
					"ErrorCode": "LocationNotAvailableForResourceType",
				},
				Message:    "Resource not available in region.",
				Suggestion: "Change region.",
			},
		},
	)

	require.NotNil(t, result,
		"Should match mockResponseError by ErrorCode property")
	assert.Equal(t, "Resource not available in region.", result.Message)
}

// --- RBAC and authorization error rule tests ---

func TestPipeline_RBACErrors(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		wantMessage string
	}{
		{
			name:        "AuthorizationFailed",
			code:        "AuthorizationFailed",
			wantMessage: "You do not have sufficient permissions for this deployment.",
		},
		{
			name:        "Unauthorized",
			code:        "Unauthorized",
			wantMessage: "The request was unauthorized.",
		},
		{
			name:        "Forbidden",
			code:        "Forbidden",
			wantMessage: "Access to this resource is forbidden.",
		},
		{
			name:        "RequestDisallowedByPolicy",
			code:        "RequestDisallowedByPolicy",
			wantMessage: "An Azure Policy is blocking this deployment.",
		},
		{
			name:        "RoleAssignmentExists",
			code:        "RoleAssignmentExists",
			wantMessage: "A role assignment with this configuration already exists.",
		},
		{
			name:        "PrincipalNotFound",
			code:        "PrincipalNotFound",
			wantMessage: "The security principal for a role assignment was not found.",
		},
		{
			name:        "NoRegisteredProviderFound",
			code:        "NoRegisteredProviderFound",
			wantMessage: "A required Azure resource provider is not registered.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := NewErrorHandlerPipeline(nil)
			err := &testDeploymentError{
				Details: &testErrorDetails{Code: tt.code},
				Title:   "deployment error: " + tt.code,
			}

			result := pipeline.ProcessWithRules(
				context.Background(),
				err,
				[]ErrorSuggestionRule{
					{
						ErrorType:  "testDeploymentError",
						Properties: map[string]string{"Details.Code": tt.code},
						Message:    tt.wantMessage,
						Suggestion: "test suggestion",
					},
				},
			)
			require.NotNil(t, result, "Should match %s", tt.code)
			assert.Equal(t, tt.wantMessage, result.Message)
		})
	}
}

func TestErrorSuggestionsYaml_IsValid(t *testing.T) {
	// Verify the embedded YAML can be parsed
	var config ErrorSuggestionsConfig
	err := yaml.Unmarshal(resources.ErrorSuggestions, &config)
	require.NoError(t, err, "error_suggestions.yaml must be valid YAML")
	require.NotEmpty(t, config.Rules, "error_suggestions.yaml must contain at least one rule")

	for i, rule := range config.Rules {
		label := fmt.Sprintf("rule[%d]", i)

		// Every rule must have at least one condition
		hasCondition := len(rule.Patterns) > 0 || rule.ErrorType != ""
		assert.True(t, hasCondition,
			"%s: must have at least one of 'patterns' or 'errorType'", label)

		// Properties require errorType
		if len(rule.Properties) > 0 {
			assert.NotEmpty(t, rule.ErrorType,
				"%s: 'properties' requires 'errorType' to be set", label)
		}

		// Every rule must produce output: either a handler or a static suggestion
		hasOutput := rule.Handler != "" || rule.Message != "" || rule.Suggestion != ""
		assert.True(t, hasOutput,
			"%s: must have at least one of 'handler', 'message', or 'suggestion'", label)

		// Regex patterns must compile
		if rule.Regex {
			for _, p := range rule.Patterns {
				_, compileErr := regexp.Compile(p)
				assert.NoError(t, compileErr,
					"%s: pattern %q must be a valid regex", label, p)
			}
			for prop, val := range rule.Properties {
				_, compileErr := regexp.Compile(val)
				assert.NoError(t, compileErr,
					"%s: property %q value %q must be a valid regex", label, prop, val)
			}
		}

		// Links must have URLs
		for j, link := range rule.Links {
			assert.NotEmpty(t, link.URL,
				"%s: links[%d] must have a 'url'", label, j)
		}
	}
}

func TestErrorSuggestionsYaml_LoadPipelineConfig(t *testing.T) {
	// Verify loadPipelineConfig succeeds and returns a usable pipeline
	pipeline := NewErrorHandlerPipeline(nil)
	require.NotNil(t, pipeline)
	assert.NotEmpty(t, pipeline.rules, "pipeline must load rules from embedded YAML")
}
