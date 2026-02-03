// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_ErrorMiddleware_SuccessNoError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(llm.FeatureLlm): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	errorSuggestionService := errorhandler.NewErrorSuggestionService()
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil, // agentFactory not needed for success case
		global,
		featureManager,
		userConfigManager,
		errorSuggestionService,
	)
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: "Success",
			},
		}, nil
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "Success", result.Message.Header)
}

func Test_ErrorMiddleware_LLMAlphaFeatureDisabled(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewEmptyConfig()
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	errorSuggestionService := errorhandler.NewErrorSuggestionService()
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorSuggestionService,
	)

	testError := errors.New("test error")
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, testError
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	// Should return error without AI intervention in no-prompt mode
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, testError, err)
}

func Test_ErrorMiddleware_ChildAction(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(llm.FeatureLlm): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	errorSuggestionService := errorhandler.NewErrorSuggestionService()
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorSuggestionService,
	)
	testError := errors.New("test error")
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, testError
	}

	// Mark context as child action
	ctx := WithChildAction(*mockContext.Context)
	result, err := middleware.Run(ctx, nextFn)

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, testError, err)
}

func Test_ErrorMiddleware_ErrorWithSuggestion(t *testing.T) {
	if os.Getenv("TF_BUILD") != "" || os.Getenv("GITHUB_ACTIONS") != "" || os.Getenv("CI") != "" {
		t.Skip("Skipping test in CI/CD environment")
	}

	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(llm.FeatureLlm): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	errorSuggestionService := errorhandler.NewErrorSuggestionService()
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorSuggestionService,
	)

	// Create error with suggestion
	testErr := errors.New("test error")
	suggestionErr := &internal.ErrorWithSuggestion{
		Err:        testErr,
		Suggestion: "Suggested fix",
	}
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, suggestionErr
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	require.Error(t, err)
	require.Nil(t, result)

	// Verify the error with suggestion is returned as-is (not modified)
	var returnedSuggestionErr *internal.ErrorWithSuggestion
	require.True(t, errors.As(err, &returnedSuggestionErr), "Expected ErrorWithSuggestion to be returned")
	require.Equal(t, "Suggested fix", returnedSuggestionErr.Suggestion)
}

func Test_ErrorMiddleware_PatternMatchingSuggestion(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewEmptyConfig()
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	errorSuggestionService := errorhandler.NewErrorSuggestionService()
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorSuggestionService,
	)

	// Create an error that matches a known pattern (quota error)
	quotaError := errors.New("Deployment failed: QuotaExceeded for resource")
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, quotaError
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	require.Error(t, err)
	require.Nil(t, result)

	// Verify the error was wrapped with a suggestion
	var suggestionErr *internal.ErrorWithSuggestion
	require.True(t, errors.As(err, &suggestionErr), "Expected error to be wrapped with suggestion")
	require.Contains(t, suggestionErr.Suggestion, "quota")
	require.NotEmpty(t, suggestionErr.DocUrl, "Expected a documentation URL")
}

func Test_ErrorMiddleware_NoPatternMatch(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewEmptyConfig()
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: true, // Use no-prompt mode to avoid AI processing
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	errorSuggestionService := errorhandler.NewErrorSuggestionService()
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorSuggestionService,
	)

	// Create an error that doesn't match any pattern
	unknownError := errors.New("some completely unique error xyz123abc")
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, unknownError
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	require.Error(t, err)
	require.Nil(t, result)

	// Verify the error was NOT wrapped with a suggestion
	var suggestionErr *internal.ErrorWithSuggestion
	require.False(t, errors.As(err, &suggestionErr), "Expected error NOT to be wrapped with suggestion")
	require.Equal(t, unknownError, err)
}

func Test_ExtractSuggestedSolutions(t *testing.T) {
	tests := []struct {
		name          string
		llmResponse   string
		expectedCount int
		expectedFirst string
	}{
		{
			name: "Valid JSON with Three Solutions",
			llmResponse: `{
				"analysis": "Brief explanation of the error",
				"solutions": [
					"Log out and log in again with Azure Developer CLI",
					"Check and fix your network environment",
					"Retry after reboot or from a clean terminal"
				]
			}`,
			expectedCount: 3,
			expectedFirst: "Log out and log in again with Azure Developer CLI",
		},
		{
			name: "Valid JSON with One Solution",
			llmResponse: `{
				"analysis": "Error analysis",
				"solutions": [
					"Only one solution"
				]
			}`,
			expectedCount: 1,
			expectedFirst: "Only one solution",
		},
		{
			name: "Valid JSON with Two Solutions",
			llmResponse: `{
				"analysis": "Error analysis",
				"solutions": [
					"First solution",
					"Second solution"
				]
			}`,
			expectedCount: 2,
			expectedFirst: "First solution",
		},
		{
			name:          "Invalid JSON",
			llmResponse:   `This is not valid JSON at all`,
			expectedCount: 0,
		},
		{
			name: "JSON with Empty Solutions Array",
			llmResponse: `{
				"analysis": "Error analysis",
				"solutions": []
			}`,
			expectedCount: 0,
		},
		{
			name: "JSON Missing Solutions Field",
			llmResponse: `{
				"analysis": "Error analysis only"
			}`,
			expectedCount: 0,
		},
		{
			name: "JSON with Extra Whitespace",
			llmResponse: `  {
				"analysis": "Error analysis",
				"solutions": [
					"  First solution with spaces  ",
					"  Second solution  "
				]
			}  `,
			expectedCount: 2,
			expectedFirst: "  First solution with spaces  ",
		},
		{
			name: "JSON Mixed with Text Before",
			llmResponse: `Here's the analysis of the error:

			{
				"analysis": "The deployment failed due to a configuration issue",
				"solutions": [
					"Update the configuration file",
					"Restart the service"
				]
			}`,
			expectedCount: 2,
			expectedFirst: "Update the configuration file",
		},
		{
			name: "JSON Mixed with Text After",
			llmResponse: `{
				"analysis": "Authentication failed",
				"solutions": [
					"Run az login to authenticate",
					"Check your subscription permissions"
				]
			}
			
			That should resolve the authentication issues.`,
			expectedCount: 2,
			expectedFirst: "Run az login to authenticate",
		},
		{
			name: "JSON Mixed with Text Before and After",
			llmResponse: `I analyzed the error and found the following:
			
			{
				"analysis": "Network connectivity issue",
				"solutions": [
					"Check network connectivity",
					"Retry with different endpoint",
					"Contact network administrator"
				]
			}
			
			Please try these solutions in order.`,
			expectedCount: 3,
			expectedFirst: "Check network connectivity",
		},
		{
			name: "JSON with Braces in Strings",
			llmResponse: `{
				"analysis": "Error contains { and } characters in message",
				"solutions": [
					"Fix the {configuration} file issue",
					"Update values in {section} configuration"
				]
			}`,
			expectedCount: 2,
			expectedFirst: "Fix the {configuration} file issue",
		},
		{
			name: "JSON with Escaped Quotes",
			llmResponse: `{
				"analysis": "String parsing error",
				"solutions": [
					"Fix the \"quoted value\" in configuration",
					"Escape the \\\"special characters\\\" properly"
				]
			}`,
			expectedCount: 2,
			expectedFirst: "Fix the \"quoted value\" in configuration",
		},
		{
			name: "JSON with Nested Objects",
			llmResponse: `{
				"analysis": "Complex configuration error",
				"metadata": {
					"severity": "high",
					"details": {
						"cause": "invalid syntax"
					}
				},
				"solutions": [
					"Fix nested configuration",
					"Validate JSON structure"
				]
			}`,
			expectedCount: 2,
			expectedFirst: "Fix nested configuration",
		},
		{
			name: "Multiple JSON Objects - First One Wins",
			llmResponse: `{
				"analysis": "First analysis",
				"solutions": [
					"First solution"
				]
			}
			{
				"analysis": "Second analysis",
				"solutions": [
					"Second solution"
				]
			}`,
			expectedCount: 1,
			expectedFirst: "First solution",
		},
		{
			name:          "Empty Response",
			llmResponse:   "",
			expectedCount: 0,
		},
		{
			name:          "Only Opening Brace",
			llmResponse:   "{",
			expectedCount: 0,
		},
		{
			name:          "Only Closing Brace",
			llmResponse:   "}",
			expectedCount: 0,
		},
		{
			name: "JSON with Line Breaks in Strings",
			llmResponse: `{
				"analysis": "Multi-line error message",
				"solutions": [
					"Fix the multi-line\nconfiguration issue",
					"Handle\r\nCRLF line endings"
				]
			}`,
			expectedCount: 2,
			expectedFirst: "Fix the multi-line\nconfiguration issue",
		},
		{
			name:          "Agent Framework Wrapped Response - Text Field with JSON String",
			llmResponse:   `{"text": "{\"analysis\": \"Error analysis\", \"solutions\": [\"S1\", \"S2\", \"S3\"]}"}`,
			expectedCount: 3,
			expectedFirst: "S1",
		},
		{
			name: "Agent Framework Wrapped Response - Text Field with Escaped JSON",
			llmResponse: `{"text": "{\"analysis\": \"The deployment failed due to insufficient permissions\", ` +
				`\"solutions\": [\"Grant Owner role to the user\", \"Use User Access Administrator role\", ` +
				`\"Contact subscription admin\"]}"}`,
			expectedCount: 3,
			expectedFirst: "Grant Owner role to the user",
		},
		{
			name:          "Agent Framework Wrapped Response - Text Field with Single Solution",
			llmResponse:   `{"text": "{\"analysis\": \"Simple error\", \"solutions\": [\"Single fix\"]}"}`,
			expectedCount: 1,
			expectedFirst: "Single fix",
		},
		{
			name:          "Agent Framework Wrapped Response - Text Field with Empty Solutions",
			llmResponse:   `{"text": "{\"analysis\": \"Error with no solutions\", \"solutions\": []}"}`,
			expectedCount: 0,
		},
		{
			name:          "Agent Framework Wrapped Response - Text Field Not a String",
			llmResponse:   `{"text": 12345}`,
			expectedCount: 0,
		},
		{
			name:          "Agent Framework Wrapped Response - Text Field is Object Not String",
			llmResponse:   `{"text": {"analysis": "nested", "solutions": ["should not extract"]}}`,
			expectedCount: 0,
		},
		{
			name:          "Agent Framework Wrapped Response - Text Field with Invalid Inner JSON",
			llmResponse:   `{"text": "this is not valid json inside"}`,
			expectedCount: 0,
		},
		{
			name: "Direct JSON Takes Precedence When Text Field Missing",
			llmResponse: `{
				"analysis": "Direct analysis",
				"solutions": ["Direct solution 1", "Direct solution 2"]
			}`,
			expectedCount: 2,
			expectedFirst: "Direct solution 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			solutions := extractSuggestedSolutions(tt.llmResponse)
			require.Equal(t, tt.expectedCount, len(solutions))

			if tt.expectedCount > 0 {
				require.Equal(t, tt.expectedFirst, solutions[0])
			}
		})
	}
}
