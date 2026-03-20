// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	surveyterm "github.com/AlecAivazis/survey/v2/terminal"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	agentcopilot "github.com/azure/azure-dev/cli/azd/internal/agent/copilot"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/pack"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
)

func Test_ErrorMiddleware_SuccessNoError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(agentcopilot.FeatureCopilot): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	errorPipeline := errorhandler.NewErrorHandlerPipeline(nil)
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil, // agentFactory not needed for success case
		global,
		featureManager,
		userConfigManager,
		errorPipeline,
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
	errorPipeline := errorhandler.NewErrorHandlerPipeline(nil)
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorPipeline,
	)

	testError := errors.New("test error")
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, testError
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	// Should return error without AI intervention when LLM alpha feature is not enabled
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, testError, err)
}

func Test_ErrorMiddleware_ChildAction(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(agentcopilot.FeatureCopilot): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	errorPipeline := errorhandler.NewErrorHandlerPipeline(nil)
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorPipeline,
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
			string(agentcopilot.FeatureCopilot): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	errorPipeline := errorhandler.NewErrorHandlerPipeline(nil)
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorPipeline,
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
	errorPipeline := errorhandler.NewErrorHandlerPipeline(nil)
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorPipeline,
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
	require.NotEmpty(t, suggestionErr.Links, "Expected reference links")
}

func Test_ErrorMiddleware_NoPatternMatch(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewEmptyConfig()
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: true, // Use no-prompt mode to avoid AI processing
	}
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	errorPipeline := errorhandler.NewErrorHandlerPipeline(nil)
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorPipeline,
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

func Test_ClassifyError(t *testing.T) {
	// --- Machine context: typed errors ---
	t.Run("MissingToolErrors classifies as MachineContext", func(t *testing.T) {
		err := &tools.MissingToolErrors{
			Errs:      []error{errors.New("docker not found")},
			ToolNames: []string{"docker"},
		}
		require.Equal(t, MachineContextError, classifyError(err))
	})

	t.Run("Wrapped MissingToolErrors classifies as MachineContext", func(t *testing.T) {
		inner := &tools.MissingToolErrors{
			Errs:      []error{errors.New("node not found")},
			ToolNames: []string{"node"},
		}
		wrapped := fmt.Errorf("setup failed: %w", inner)
		require.Equal(t, MachineContextError, classifyError(wrapped))
	})

	t.Run("ErrSemver classifies as MachineContext", func(t *testing.T) {
		err := &tools.ErrSemver{
			ToolName: "node",
			VersionInfo: tools.VersionInfo{
				MinimumVersion: semver.MustParse("18.0.0"),
				UpdateCommand:  "nvm install",
			},
		}
		require.Equal(t, MachineContextError, classifyError(err))
	})

	t.Run("ExtensionRunError classifies as MachineContext", func(t *testing.T) {
		err := &extensions.ExtensionRunError{
			ExtensionId: "my-extension",
			Err:         errors.New("extension crashed"),
		}
		require.Equal(t, MachineContextError, classifyError(err))
	})

	t.Run("StatusCodeError classifies as MachineContext", func(t *testing.T) {
		err := &pack.StatusCodeError{
			Code: 1,
			Err:  errors.New("pack build failed"),
		}
		require.Equal(t, MachineContextError, classifyError(err))
	})

	// --- User context: typed errors ---
	t.Run("ReLoginRequiredError classifies as UserContext", func(t *testing.T) {
		err := &auth.ReLoginRequiredError{}
		require.Equal(t, UserContextError, classifyError(err))
	})

	t.Run("AuthFailedError classifies as UserContext", func(t *testing.T) {
		err := &auth.AuthFailedError{}
		require.Equal(t, UserContextError, classifyError(err))
	})

	userContextSentinels := []struct {
		name string
		err  error
	}{
		// auth
		{"auth.ErrNoCurrentUser", auth.ErrNoCurrentUser},
		// azapi
		{"azapi.ErrAzCliNotLoggedIn", azapi.ErrAzCliNotLoggedIn},
		{"azapi.ErrAzCliRefreshTokenExpired", azapi.ErrAzCliRefreshTokenExpired},
		// github
		{"github.ErrGitHubCliNotLoggedIn", github.ErrGitHubCliNotLoggedIn},
		{"github.ErrUserNotAuthorized", github.ErrUserNotAuthorized},
		{"github.ErrRepositoryNameInUse", github.ErrRepositoryNameInUse},
		// environment
		{"environment.ErrNotFound", environment.ErrNotFound},
		{"environment.ErrNameNotSpecified", environment.ErrNameNotSpecified},
		{"environment.ErrDefaultEnvironmentNotFound", environment.ErrDefaultEnvironmentNotFound},
		{"environment.ErrAccessDenied", environment.ErrAccessDenied},
		// pipeline
		{"pipeline.ErrAuthNotSupported", pipeline.ErrAuthNotSupported},
		{"pipeline.ErrRemoteHostIsNotAzDo", pipeline.ErrRemoteHostIsNotAzDo},
		{"pipeline.ErrSSHNotSupported", pipeline.ErrSSHNotSupported},
		{"pipeline.ErrRemoteHostIsNotGitHub", pipeline.ErrRemoteHostIsNotGitHub},
		// project
		{"project.ErrNoDefaultService", project.ErrNoDefaultService},
	}

	for _, tc := range userContextSentinels {
		t.Run(tc.name+" classifies as UserContext", func(t *testing.T) {
			require.Equal(t, UserContextError, classifyError(tc.err))
		})

		t.Run("Wrapped "+tc.name+" classifies as UserContext", func(t *testing.T) {
			wrapped := fmt.Errorf("operation failed: %w", tc.err)
			require.Equal(t, UserContextError, classifyError(wrapped))
		})
	}

	// --- Azure context: defaults ---
	t.Run("Generic error defaults to AzureContext", func(t *testing.T) {
		err := errors.New("deploying to Azure: InternalServerError")
		require.Equal(t, AzureContextAndOtherError, classifyError(err))
	})
}

func Test_ShouldSkipErrorAnalysis(t *testing.T) {
	t.Run("Wrapped context.Canceled is skipped", func(t *testing.T) {
		wrapped := fmt.Errorf("operation aborted: %w", context.Canceled)
		require.True(t, shouldSkipErrorAnalysis(wrapped))
	})

	t.Run("Wrapped InterruptErr is skipped", func(t *testing.T) {
		wrapped := fmt.Errorf("prompt failed: %w", surveyterm.InterruptErr)
		require.True(t, shouldSkipErrorAnalysis(wrapped))
	})
}

func Test_TroubleshootCategory_Constants(t *testing.T) {
	// Verify constant values match expected strings used in config
	require.Equal(t, troubleshootCategory("explain"), categoryExplain)
	require.Equal(t, troubleshootCategory("guidance"), categoryGuidance)
	require.Equal(t, troubleshootCategory("troubleshoot"), categoryTroubleshoot)
	require.Equal(t, troubleshootCategory("skip"), categorySkip)
}

func Test_BuildPromptForCategory(t *testing.T) {
	middleware := &ErrorMiddleware{
		options: &Options{CommandPath: "azd provision"},
	}
	testErr := errors.New("deployment failed: QuotaExceeded")

	tests := []struct {
		name     string
		category troubleshootCategory
		contains []string
	}{
		{
			name:     "explain category",
			category: categoryExplain,
			contains: []string{"azd provision", "QuotaExceeded", "EXPLAIN TO THE USER", "What happened"},
		},
		{
			name:     "guidance category",
			category: categoryGuidance,
			contains: []string{"azd provision", "QuotaExceeded", "actionable fix steps"},
		},
		{
			name:     "troubleshoot category",
			category: categoryTroubleshoot,
			contains: []string{"azd provision", "QuotaExceeded", "EXPLAIN TO THE USER", "RECOMMEND MANUAL STEPS"},
		},
		{
			name:     "default falls back to troubleshoot manual",
			category: troubleshootCategory("unknown"),
			contains: []string{"azd provision", "QuotaExceeded", "EXPLAIN TO THE USER", "RECOMMEND MANUAL STEPS"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := middleware.buildPromptForCategory(tt.category, testErr)
			for _, s := range tt.contains {
				require.Contains(t, prompt, s)
			}
		})
	}
}

func Test_BuildFixPrompt(t *testing.T) {
	middleware := &ErrorMiddleware{
		options: &Options{CommandPath: "azd up"},
	}
	testErr := errors.New("resource group not found")

	prompt := middleware.buildFixPrompt(testErr)
	require.Contains(t, prompt, "azd up")
	require.Contains(t, prompt, "resource group not found")
	require.Contains(t, prompt, "FIX")
	require.Contains(t, prompt, "minimal change")
}

func Test_ConfigKeyErrorHandlingCategory(t *testing.T) {
	// Verify the config key is properly namespaced
	require.Equal(t, "copilot.errorHandling.category", agentcopilot.ConfigKeyErrorHandlingCategory)
}
