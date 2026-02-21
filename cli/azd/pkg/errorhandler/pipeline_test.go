// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"context"
	"errors"
	"fmt"
	"testing"

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
