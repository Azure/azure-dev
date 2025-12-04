// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
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
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil, // agentFactory not needed for success case
		global,
		featureManager,
		userConfigManager,
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
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
	)

	testError := errors.New("test error")
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, testError
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, testError, err)
}

func Test_ErrorMiddleware_NoPromptMode(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(llm.FeatureLlm): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: true, // Non-interactive mode
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
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
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
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
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
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

	// Check that suggestion was displayed
	consoleOutput := mockContext.Console.Output()
	foundSuggestion := false
	for _, message := range consoleOutput {
		if message == "Suggested fix" {
			foundSuggestion = true
			break
		}
	}
	require.True(t, foundSuggestion, "No suggestion displayed for ErrorWithSuggestion")
}

func Test_ExtractSuggestedSolutions(t *testing.T) {
	tests := []struct {
		name          string
		llmResponse   string
		expectedCount int
		expectedFirst string
	}{
		{
			name: "Three Solutions Found",
			llmResponse: `## Brainstorm Solutions
1. Log out and log in again with Azure Developer CLI
2. Check and fix your network environment
3. Retry after reboot or from a clean terminal`,
			expectedCount: 3,
			expectedFirst: "Log out and log in again with Azure Developer CLI",
		},
		{
			name: "Solutions with Extra Whitespace",
			llmResponse: `### Brainstorm Solutions
   1. First solution here
   2. Second solution here
   3. Third solution here`,
			expectedCount: 3,
			expectedFirst: "First solution here",
		},
		{
			name: "Solutions with Other Sections",
			llmResponse: `## Error Analysis
This is the error analysis.

## Brainstorm Solutions
1. Solution one
2. Solution two
3. Solution three

## Additional Information
More details here.`,
			expectedCount: 3,
			expectedFirst: "Solution one",
		},
		{
			name: "No Solutions Section",
			llmResponse: `## Error Analysis
This is just an analysis without solutions.`,
			expectedCount: 0,
		},
		{
			name: "Less Than Three Solutions",
			llmResponse: `## Brainstorm Solutions
1. Only one solution`,
			expectedCount: 1,
			expectedFirst: "Only one solution",
		},
		{
			name: "More Than Three Solutions (Should Stop at 3)",
			llmResponse: `## Brainstorm Solutions
1. Solution one
2. Solution two
3. Solution three
4. Solution four
5. Solution five`,
			expectedCount: 3,
			expectedFirst: "Solution one",
		},
		{
			name: "Case Insensitive Section Header",
			llmResponse: `## brainstorm solutions
1. First solution
2. Second solution`,
			expectedCount: 2,
			expectedFirst: "First solution",
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
