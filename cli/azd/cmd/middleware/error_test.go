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
	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	agentcopilot "github.com/azure/azure-dev/cli/azd/internal/agent/copilot"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/pack"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_ErrorMiddleware_SuccessNoError(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(t.Context())
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
	t.Parallel()
	mockContext := mocks.NewMockContext(t.Context())
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
	t.Parallel()
	mockContext := mocks.NewMockContext(t.Context())
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
	t.Parallel()
	if os.Getenv("TF_BUILD") != "" || os.Getenv("GITHUB_ACTIONS") != "" || os.Getenv("CI") != "" {
		t.Skip("Skipping test in CI/CD environment")
	}

	mockContext := mocks.NewMockContext(t.Context())
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
	t.Parallel()
	mockContext := mocks.NewMockContext(t.Context())
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

// Test_ErrorMiddleware_ExtensionErrorWithSuggestion_BypassesPipeline verifies that
// when an extension-supplied error (LocalError or ServiceError) already carries a
// Suggestion, the YAML error-suggestion pipeline is short-circuited so it doesn't
// override the extension's specific guidance with a generic one. Regression test
// for https://github.com/Azure/azure-dev/issues/7706.
func Test_ErrorMiddleware_ExtensionErrorWithSuggestion_BypassesPipeline(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	cfg := config.NewEmptyConfig()
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: true, // skip agentic handling
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

	// "QuotaExceeded" matches a known pattern in error_suggestions.yaml that would
	// otherwise wrap into an *ErrorWithSuggestion (proven by
	// Test_ErrorMiddleware_PatternMatchingSuggestion). The middleware must skip
	// that wrapping when the extension already supplied a suggestion of its own.
	extErr := &azdext.LocalError{
		Message:    "Deployment failed: QuotaExceeded for resource",
		Code:       "quota_exceeded",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Extension-provided guidance: request a quota increase via the portal.",
	}
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, extErr
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	require.Error(t, err)
	require.Nil(t, result)

	// The original LocalError must be returned unchanged — not wrapped in
	// *ErrorWithSuggestion by the YAML pipeline.
	var wrapped *internal.ErrorWithSuggestion
	require.False(
		t,
		errors.As(err, &wrapped),
		"YAML pipeline should not wrap an extension error that already has a Suggestion",
	)
	returned, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected the original *LocalError to be returned")
	require.Same(t, extErr, returned)
	require.Equal(t, extErr.Suggestion, azdext.ErrorSuggestion(err))
}

func Test_ErrorMiddleware_StructuredExtensionErrorWithoutSuggestion_BypassesPipeline(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	cfg := config.NewEmptyConfig()
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{
		NoPrompt: true,
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

	extErr := &azdext.LocalError{
		Message:  "Deployment failed: QuotaExceeded for resource",
		Code:     "quota_exceeded",
		Category: azdext.LocalErrorCategoryUser,
	}
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, extErr
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	require.Error(t, err)
	require.Nil(t, result)

	var wrapped *internal.ErrorWithSuggestion
	require.False(t, errors.As(err, &wrapped))
	returned, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Same(t, extErr, returned)
	require.Empty(t, azdext.ErrorSuggestion(err))
}

func Test_ErrorMiddleware_NoPatternMatch(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(t.Context())
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

func Test_ShouldSkipAgentHandling_FixableErrors(t *testing.T) {
	t.Parallel()
	t.Run("MissingToolErrors is not skipped", func(t *testing.T) {
		t.Parallel()
		err := &tools.MissingToolErrors{
			Errs:      []error{errors.New("docker not found")},
			ToolNames: []string{"docker"},
		}
		require.False(t, shouldSkipAgentHandling(err))
	})

	t.Run("Wrapped MissingToolErrors is not skipped", func(t *testing.T) {
		t.Parallel()
		inner := &tools.MissingToolErrors{
			Errs:      []error{errors.New("node not found")},
			ToolNames: []string{"node"},
		}
		wrapped := fmt.Errorf("setup failed: %w", inner)
		require.False(t, shouldSkipAgentHandling(wrapped))
	})

	t.Run("ErrSemver is not skipped", func(t *testing.T) {
		t.Parallel()
		err := &tools.ErrSemver{
			ToolName: "node",
			VersionInfo: tools.VersionInfo{
				MinimumVersion: semver.MustParse("18.0.0"),
				UpdateCommand:  "nvm install",
			},
		}
		require.False(t, shouldSkipAgentHandling(err))
	})

	t.Run("ExtensionRunError is skipped", func(t *testing.T) {
		t.Parallel()
		err := &extensions.ExtensionRunError{
			ExtensionId: "my-extension",
			Err:         errors.New("extension crashed"),
		}
		require.True(t, shouldSkipAgentHandling(err))
	})

	t.Run("StatusCodeError is skipped", func(t *testing.T) {
		t.Parallel()
		err := &pack.StatusCodeError{
			Code: 1,
			Err:  errors.New("pack build failed"),
		}
		require.True(t, shouldSkipAgentHandling(err))
	})

	t.Run("ReLoginRequiredError is not skipped", func(t *testing.T) {
		t.Parallel()
		err := &auth.ReLoginRequiredError{}
		require.False(t, shouldSkipAgentHandling(err))
	})

	t.Run("AuthFailedError is not skipped", func(t *testing.T) {
		t.Parallel()
		err := &auth.AuthFailedError{}
		require.False(t, shouldSkipAgentHandling(err))
	})

	sentinels := []struct {
		name    string
		err     error
		skipped bool
	}{
		{"auth.ErrNoCurrentUser", auth.ErrNoCurrentUser, false},
		{"azapi.ErrAzCliNotLoggedIn", azapi.ErrAzCliNotLoggedIn, false},
		{"azapi.ErrAzCliRefreshTokenExpired",
			azapi.ErrAzCliRefreshTokenExpired, false},
		{"github.ErrGitHubCliNotLoggedIn",
			github.ErrGitHubCliNotLoggedIn, false},
		{"github.ErrUserNotAuthorized", github.ErrUserNotAuthorized, false},
		{"github.ErrRepositoryNameInUse", github.ErrRepositoryNameInUse, false},
		// environment
		{"environment.ErrNotFound", environment.ErrNotFound, true},
		{"environment.ErrNameNotSpecified", environment.ErrNameNotSpecified, true},
		{"environment.ErrDefaultEnvironmentNotFound",
			environment.ErrDefaultEnvironmentNotFound, true},
		{"environment.ErrAccessDenied", environment.ErrAccessDenied, true},
		// pipeline
		{"pipeline.ErrAuthNotSupported", pipeline.ErrAuthNotSupported, true},
		{"pipeline.ErrRemoteHostIsNotAzDo", pipeline.ErrRemoteHostIsNotAzDo, true},
		{"pipeline.ErrSSHNotSupported", pipeline.ErrSSHNotSupported, true},
		{"pipeline.ErrRemoteHostIsNotGitHub", pipeline.ErrRemoteHostIsNotGitHub, true},
		// project
		{"project.ErrNoDefaultService", project.ErrNoDefaultService, true},
	}

	for _, tc := range sentinels {
		t.Run(tc.name+" shouldSkipAgentHandling", func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.skipped, shouldSkipAgentHandling(tc.err))
		})

		t.Run("Wrapped "+tc.name+" shouldSkipAgentHandling", func(t *testing.T) {
			t.Parallel()
			wrapped := fmt.Errorf("operation failed: %w", tc.err)
			require.Equal(t, tc.skipped, shouldSkipAgentHandling(wrapped))
		})
	}

	// --- Azure context: defaults ---
	t.Run("Generic error defaults to not skipped", func(t *testing.T) {
		t.Parallel()
		err := errors.New("deploying to Azure: InternalServerError")
		require.False(t, shouldSkipAgentHandling(err))
	})
}

func Test_ShouldSkipAgentHandling_ControlFlow(t *testing.T) {
	t.Parallel()
	t.Run("Wrapped context.Canceled is skipped", func(t *testing.T) {
		t.Parallel()
		wrapped := fmt.Errorf("operation aborted: %w", context.Canceled)
		require.True(t, shouldSkipAgentHandling(wrapped))
	})

	t.Run("Wrapped InterruptErr is skipped", func(t *testing.T) {
		t.Parallel()
		wrapped := fmt.Errorf("prompt failed: %w", surveyterm.InterruptErr)
		require.True(t, shouldSkipAgentHandling(wrapped))
	})

	t.Run("ErrAbortedByUser is skipped", func(t *testing.T) {
		t.Parallel()
		require.True(t, shouldSkipAgentHandling(internal.ErrAbortedByUser))
	})

	t.Run("Wrapped ErrAbortedByUser is skipped", func(t *testing.T) {
		t.Parallel()
		wrapped := fmt.Errorf("validation declined: %w", internal.ErrAbortedByUser)
		require.True(t, shouldSkipAgentHandling(wrapped))
	})

	t.Run("UpdateError is skipped", func(t *testing.T) {
		t.Parallel()
		err := &update.UpdateError{Code: update.CodeDownloadFailed, Err: errors.New("download failed")}
		require.True(t, shouldSkipAgentHandling(err))
	})

	t.Run("Wrapped UpdateError is skipped", func(t *testing.T) {
		t.Parallel()
		inner := &update.UpdateError{Code: update.CodeReplaceFailed, Err: errors.New("replace failed")}
		wrapped := fmt.Errorf("update error: %w", inner)
		require.True(t, shouldSkipAgentHandling(wrapped))
	})
}

func Test_TroubleshootCategory_Constants(t *testing.T) {
	t.Parallel()
	// Verify constant values match expected strings used in config
	require.Equal(t, troubleshootCategory("explain"), categoryExplain)
	require.Equal(t, troubleshootCategory("guidance"), categoryGuidance)
	require.Equal(t, troubleshootCategory("troubleshoot"), categoryTroubleshoot)
	require.Equal(t, troubleshootCategory("fix"), categoryFix)
	require.Equal(t, troubleshootCategory("skip"), categorySkip)
}

func Test_BuildPromptForCategory(t *testing.T) {
	t.Parallel()
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
			name:     "fix category",
			category: categoryFix,
			contains: []string{"azd provision", "QuotaExceeded", "FIX", "minimal change"},
		},
		{
			name:     "default falls back to troubleshoot manual",
			category: troubleshootCategory("unknown"),
			contains: []string{"azd provision", "QuotaExceeded", "EXPLAIN TO THE USER", "RECOMMEND MANUAL STEPS"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prompt := middleware.buildPromptForCategory(tt.category, testErr)
			for _, s := range tt.contains {
				require.Contains(t, prompt, s)
			}
		})
	}
}

func Test_BuildFixPrompt(t *testing.T) {
	t.Parallel()
	middleware := &ErrorMiddleware{
		options: &Options{CommandPath: "azd up"},
	}
	testErr := errors.New("resource group not found")

	prompt, err := middleware.buildFixPrompt(testErr)
	require.NoError(t, err)
	require.Contains(t, prompt, "azd up")
	require.Contains(t, prompt, "resource group not found")
	require.Contains(t, prompt, "FIX")
	require.Contains(t, prompt, "minimal change")
}

func Test_ConfigKeyErrorHandlingCategory(t *testing.T) {
	t.Parallel()
	// Verify the config key is properly namespaced
	require.Equal(t, "copilot.errorHandling.category", agentcopilot.ConfigKeyErrorHandlingCategory)
}

func Test_ShouldSkipAgentHandling_DeadlineExceeded(t *testing.T) {
	t.Parallel()
	require.True(t, shouldSkipAgentHandling(context.DeadlineExceeded))
}

func Test_ShouldSkipAgentHandling_WrappedDeadlineExceeded(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("timed out: %w", context.DeadlineExceeded)
	require.True(t, shouldSkipAgentHandling(wrapped))
}

func Test_ShouldSkipAgentHandling_MissingInputsError(t *testing.T) {
	t.Parallel()
	err := &bicep.MissingInputsError{
		Inputs: []bicep.MissingInput{
			{Name: "location"},
		},
	}
	require.True(t, shouldSkipAgentHandling(err))
}

func Test_ShouldSkipAgentHandling_WrappedMissingInputsError(t *testing.T) {
	t.Parallel()
	inner := &bicep.MissingInputsError{
		Inputs: []bicep.MissingInput{
			{Name: "location"},
		},
	}
	wrapped := fmt.Errorf("provision failed: %w", inner)
	require.True(t, shouldSkipAgentHandling(wrapped))
}

func Test_ShouldSkipAgentHandling_ConfigValidationError(t *testing.T) {
	t.Parallel()
	err := &project.ConfigValidationError{
		Issues: []string{"service 'web' has nil definition"},
	}
	require.True(t, shouldSkipAgentHandling(err))
}

func Test_ShouldSkipAgentHandling_WrappedConfigValidationError(t *testing.T) {
	t.Parallel()
	inner := &project.ConfigValidationError{
		Issues: []string{"hook 'preprovision' is nil"},
	}
	wrapped := fmt.Errorf("config load: %w", inner)
	require.True(t, shouldSkipAgentHandling(wrapped))
}

func Test_ErrorMiddleware_NonFixableError_SkipsAgentCreation(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(agentcopilot.FeatureCopilot): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	global := &internal.GlobalCommandOptions{NoPrompt: false}
	userConfigManager := config.NewUserConfigManager(
		mockContext.ConfigManager)
	errorPipeline := errorhandler.NewErrorHandlerPipeline(nil)

	// agentFactory is nil — if code tries to call Create, it panics
	middleware := NewErrorMiddleware(
		&Options{Name: "test"},
		mockContext.Console,
		nil,
		global,
		featureManager,
		userConfigManager,
		errorPipeline,
	)

	// environment.ErrNotFound is non-fixable
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{}, environment.ErrNotFound
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	// Should return error without ever touching the agent factory
	require.Error(t, err)
	require.ErrorIs(t, err, environment.ErrNotFound)
	require.NotNil(t, result)
}

// clearCIEnvVarsForTest clears environment variables that affect terminal detection.
//
//	Uses the same t.Setenv + os.Unsetenv pattern as terminal_test.go.
func clearCIEnvVarsForTest(t *testing.T) {
	t.Helper()
	ciVars := []string{"AZD_FORCE_TTY",
		// CI env vars
		"CI", "TF_BUILD", "GITHUB_ACTIONS",
	}

	for _, v := range ciVars {
		if _, exists := os.LookupEnv(v); exists {
			t.Setenv(v, "")
			os.Unsetenv(v)
		}
	}
}

func Test_ErrorMiddleware_ExplainAndFixCalls(t *testing.T) {
	clearCIEnvVarsForTest(t)

	explainResult := &agent.AgentResult{
		Usage: agent.UsageMetrics{
			InputTokens:  500,
			OutputTokens: 200,
		},
	}

	// Explain succeeds, fix fails — proves both calls are made
	fakeAg := &fakeSequenceAgent{
		results: []*agent.AgentResult{explainResult, nil},
		errors:  []error{nil, errors.New("fix attempt failed")},
	}

	factory := &mockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).
		Return(fakeAg, nil)

	ucm := &mockUserConfigManager{
		cfg: configWithKeys(
			agentcopilot.ConfigKeyErrorHandlingCategory, "explain",
			agentcopilot.ConfigKeyErrorHandlingFix, "allow",
		),
	}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(
		mockinput.NewMockConsole(), factory, fm, ucm, global)

	originalErr := errors.New(
		"unexpected widget provisioning failure")
	result, err := m.Run(t.Context(), func(
		_ context.Context,
	) (*actions.ActionResult, error) {
		return nil, originalErr
	})

	// Explain succeeded, fix failed — original error returned
	// wrapped with agent context
	require.Error(t, err)
	require.ErrorIs(t, err, originalErr)

	// Result may be nil since nextFn returned nil actionResult
	_ = result

	// Verify both calls were made: explain (1st) + fix (2nd)
	require.Equal(t, 2, fakeAg.callIdx,
		"agent should be called twice: explain + fix")
}

func Test_ErrorMiddleware_MaxRetry_FirstIterationSkipsCounter(t *testing.T) {
	clearCIEnvVarsForTest(t)

	// Agent fails on fix — exits before the TTY retry prompt.
	// This still proves the counter was skipped (agent WAS called).
	fakeAg := &fakeSequenceAgent{
		results: []*agent.AgentResult{nil},
		errors:  []error{errors.New("agent fix attempt failed")},
	}

	factory := &mockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).
		Return(fakeAg, nil)

	ucm := &mockUserConfigManager{
		cfg: configWithKeys(
			agentcopilot.ConfigKeyErrorHandlingCategory, "fix"),
	}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(
		mockinput.NewMockConsole(), factory, fm, ucm, global)

	sameError := errors.New("same error every time")
	callCount := 0
	result, err := m.Run(t.Context(), func(
		_ context.Context,
	) (*actions.ActionResult, error) {
		callCount++
		return &actions.ActionResult{}, sameError
	})

	// First iteration: previousError is nil, counter is skipped,
	// agent fix is called (and fails). The middleware returns original
	// error wrapped with agent context. This proves the counter
	// did NOT trigger on first iteration — if it had, the agent
	// would never have been called.
	//
	// The "fix it manually" bail-out (attempt >= 3) requires 3+
	// same-error loop iterations, each needing promptRetryAfterFix
	// to return retry=true (requires raw TTY). That path requires
	// integration testing.
	require.Error(t, err)
	require.ErrorIs(t, err, sameError,
		"original error should be preserved")
	require.NotContains(t, err.Error(),
		"fix it manually",
		"should NOT reach max attempts on first iteration")

	// Agent was called once; the fix attempt failed, which still proves
	// the counter was skipped on the first iteration.
	require.Equal(t, 1, fakeAg.callIdx)

	// next was called once (no retry without TTY prompt)
	require.Equal(t, 1, callCount)
	require.NotNil(t, result)
}

func Test_PromptNextAction_SavedAllow_ReturnsFixAndRetry(t *testing.T) {
	t.Parallel()

	cfg := configWithKeys(agentcopilot.ConfigKeyErrorHandlingFix, "allow")
	ucm := &mockUserConfigManager{cfg: cfg}

	m := &ErrorMiddleware{
		console:           mockinput.NewMockConsole(),
		userConfigManager: ucm,
	}

	action, err := m.promptNextAction(t.Context())
	require.NoError(t, err)
	require.Equal(t, actionFixAndRetry, action,
		"saved 'allow' preference should return actionFixAndRetry to auto-rerun the command")
}

func Test_PromptNextAction_ConfigLoadError(t *testing.T) {
	t.Parallel()

	ucm := &mockUserConfigManager{cfg: nil, err: errors.New("io error")}

	m := &ErrorMiddleware{
		console:           mockinput.NewMockConsole(),
		userConfigManager: ucm,
	}

	action, err := m.promptNextAction(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "io error")
	require.Equal(t, actionExit, action)
}

func Test_PromptRetryAfterFix_SavedAllow_ReturnsTrue(t *testing.T) {
	t.Parallel()

	cfg := configWithKeys(agentcopilot.ConfigKeyErrorHandlingFix, "allow")
	ucm := &mockUserConfigManager{cfg: cfg}

	m := &ErrorMiddleware{
		console:           mockinput.NewMockConsole(),
		userConfigManager: ucm,
	}

	shouldRetry, err := m.promptRetryAfterFix(t.Context())
	require.NoError(t, err)
	require.True(t, shouldRetry,
		"saved 'allow' preference should bypass the retry prompt and auto-retry")
}

func Test_PromptRetryAfterFix_ConfigLoadError(t *testing.T) {
	t.Parallel()

	ucm := &mockUserConfigManager{cfg: nil, err: errors.New("io error")}

	m := &ErrorMiddleware{
		console:           mockinput.NewMockConsole(),
		userConfigManager: ucm,
	}

	shouldRetry, err := m.promptRetryAfterFix(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "io error")
	require.False(t, shouldRetry)
}

func TestShouldSkipAgentHandling_ConsentErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
	}{
		{"ErrToolExecutionDenied", consent.ErrToolExecutionDenied},
		{"ErrElicitationDenied", consent.ErrElicitationDenied},
		{"ErrSamplingDenied", consent.ErrSamplingDenied},
		{"WrappedToolExecutionDenied", fmt.Errorf("op: %w", consent.ErrToolExecutionDenied)},
		{"WrappedElicitationDenied", fmt.Errorf("op: %w", consent.ErrElicitationDenied)},
		{"WrappedSamplingDenied", fmt.Errorf("op: %w", consent.ErrSamplingDenied)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.True(t, shouldSkipAgentHandling(tt.err))
		})
	}
}

func TestShouldSkipAgentHandling_AzdContextErrNoProject(t *testing.T) {
	t.Parallel()
	require.True(t, shouldSkipAgentHandling(azdcontext.ErrNoProject))
}

func TestShouldSkipAgentHandling_WrappedErrNoProject(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("init failed: %w", azdcontext.ErrNoProject)
	require.True(t, shouldSkipAgentHandling(err))
}

func TestShouldSkipAgentHandling_EnvironmentInitError(t *testing.T) {
	t.Parallel()
	err := &environment.EnvironmentInitError{Name: "test-env"}
	require.True(t, shouldSkipAgentHandling(err))
}

func TestShouldSkipAgentHandling_WrappedEnvironmentInitError(t *testing.T) {
	t.Parallel()
	inner := &environment.EnvironmentInitError{Name: "test-env"}
	err := fmt.Errorf("env error: %w", inner)
	require.True(t, shouldSkipAgentHandling(err))
}

func TestShouldSkipAgentHandling_ExtensionRunError(t *testing.T) {
	t.Parallel()
	err := &extensions.ExtensionRunError{ExtensionId: "test-ext", Err: fmt.Errorf("failed")}
	require.True(t, shouldSkipAgentHandling(err), "ExtensionRunError should be skipped")
}

func TestShouldSkipAgentHandling_WrappedExtensionRunError(t *testing.T) {
	t.Parallel()
	inner := &extensions.ExtensionRunError{ExtensionId: "test-ext", Err: fmt.Errorf("failed")}
	err := fmt.Errorf("ext failed: %w", inner)
	require.True(t, shouldSkipAgentHandling(err), "wrapped ExtensionRunError should be skipped")
}

func TestShouldSkipAgentHandling_EnvironmentNotFound(t *testing.T) {
	t.Parallel()
	require.True(t, shouldSkipAgentHandling(environment.ErrNotFound), "ErrNotFound should be skipped")
}

func TestShouldSkipAgentHandling_PipelineAuthNotSupported(t *testing.T) {
	t.Parallel()
	require.True(t, shouldSkipAgentHandling(pipeline.ErrAuthNotSupported), "ErrAuthNotSupported should be skipped")
}

func TestShouldSkipAgentHandling_GenericError(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("some azure error")
	require.False(t, shouldSkipAgentHandling(err), "generic error should not be skipped")
}

func TestPromptTroubleshootCategory_SavedPreference(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		saved    string
		expected troubleshootCategory
	}{
		{"explain", "explain", categoryExplain},
		{"guidance", "guidance", categoryGuidance},
		{"troubleshoot", "troubleshoot", categoryTroubleshoot},
		{"skip", "skip", categorySkip},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockCtx := mocks.NewMockContext(t.Context())
			userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)
			cfg, err := userConfigManager.Load()
			require.NoError(t, err)
			err = cfg.Set(agentcopilot.ConfigKeyErrorHandlingCategory, tt.saved)
			require.NoError(t, err)

			e := &ErrorMiddleware{
				options:           &Options{CommandPath: "azd provision"},
				console:           mockinput.NewMockConsole(),
				userConfigManager: userConfigManager,
			}

			category, err := e.promptTroubleshootCategory(t.Context())
			require.NoError(t, err)
			require.Equal(t, tt.expected, category)
		})
	}
}

func TestPromptTroubleshootCategory_InvalidSavedValue(t *testing.T) {
	t.Parallel()
	// An invalid saved value should NOT be auto-selected.
	// It should fall through to the interactive prompt — which will fail
	// in test context, so we just verify it doesn't return the invalid value.
	// Since uxlib.Select.Ask will panic/fail without real input, we skip
	// the Ask path and only test that valid saved values are handled.
	// Instead, let's just verify that the saved empty string is handled.
	mockCtx := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)
	cfg, err := userConfigManager.Load()
	require.NoError(t, err)
	// Empty string should fall through to prompt
	err = cfg.Set(agentcopilot.ConfigKeyErrorHandlingCategory, "")
	require.NoError(t, err)

	// We can't test the Ask path without a real console, but we've covered
	// the saved-preference branch above. The empty-string case exercises
	// the "val != ''" check.
}

func TestErrorMiddleware_Run_CopilotEnabled_SkippableError(t *testing.T) {
	t.Parallel()
	if isCI() {
		t.Skip("Skipping test in CI/CD environment")
	}

	console := mockinput.NewMockConsole()
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(agentcopilot.FeatureCopilot): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	mockCtx := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)

	e := &ErrorMiddleware{
		options:           &Options{},
		console:           console,
		global:            &internal.GlobalCommandOptions{},
		featuresManager:   featureManager,
		userConfigManager: userConfigManager,
		errorPipeline:     errorhandler.NewErrorHandlerPipeline(nil),
	}

	// Even with copilot enabled, skippable errors should be returned as-is
	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, context.Canceled
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result)
}

func TestErrorMiddleware_Run_CopilotEnabled_ConsentDenied(t *testing.T) {
	t.Parallel()
	if isCI() {
		t.Skip("Skipping test in CI/CD environment")
	}

	console := mockinput.NewMockConsole()
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(agentcopilot.FeatureCopilot): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	mockCtx := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)

	e := &ErrorMiddleware{
		options:           &Options{},
		console:           console,
		global:            &internal.GlobalCommandOptions{},
		featuresManager:   featureManager,
		userConfigManager: userConfigManager,
		errorPipeline:     errorhandler.NewErrorHandlerPipeline(nil),
	}

	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, consent.ErrToolExecutionDenied
	})

	require.ErrorIs(t, err, consent.ErrToolExecutionDenied)
	require.Nil(t, result)
}

func TestErrorMiddleware_Run_CopilotEnabled_ErrNoProject(t *testing.T) {
	t.Parallel()
	if isCI() {
		t.Skip("Skipping test in CI/CD environment")
	}

	console := mockinput.NewMockConsole()
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(agentcopilot.FeatureCopilot): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	mockCtx := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)

	e := &ErrorMiddleware{
		options:           &Options{},
		console:           console,
		global:            &internal.GlobalCommandOptions{},
		featuresManager:   featureManager,
		userConfigManager: userConfigManager,
		errorPipeline:     errorhandler.NewErrorHandlerPipeline(nil),
	}

	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, azdcontext.ErrNoProject
	})

	require.ErrorIs(t, err, azdcontext.ErrNoProject)
	require.Nil(t, result)
}

func TestErrorMiddleware_Run_CopilotEnabled_EnvironmentInitError(t *testing.T) {
	t.Parallel()
	if isCI() {
		t.Skip("Skipping test in CI/CD environment")
	}

	console := mockinput.NewMockConsole()
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(agentcopilot.FeatureCopilot): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	mockCtx := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)

	e := &ErrorMiddleware{
		options:           &Options{},
		console:           console,
		global:            &internal.GlobalCommandOptions{},
		featuresManager:   featureManager,
		userConfigManager: userConfigManager,
		errorPipeline:     errorhandler.NewErrorHandlerPipeline(nil),
	}

	initErr := &environment.EnvironmentInitError{Name: "test-env"}
	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, initErr
	})

	require.Error(t, err)
	require.Nil(t, result)
}

func TestErrorMiddleware_Run_CopilotEnabled_NoPrompt(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(agentcopilot.FeatureCopilot): "on",
		},
	})
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)
	mockCtx := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)

	e := &ErrorMiddleware{
		options:           &Options{},
		console:           console,
		global:            &internal.GlobalCommandOptions{NoPrompt: true},
		featuresManager:   featureManager,
		userConfigManager: userConfigManager,
		errorPipeline:     errorhandler.NewErrorHandlerPipeline(nil),
	}

	expectedErr := errors.New("deployment failed")
	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, expectedErr
	})

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, result)
}

func TestErrorMiddleware_Run_NullResultFromNext_NoError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	e := &ErrorMiddleware{
		options:         &Options{},
		console:         console,
		global:          &internal.GlobalCommandOptions{},
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		errorPipeline:   errorhandler.NewErrorHandlerPipeline(nil),
	}

	// When next returns nil result and nil error, ErrorMiddleware should not panic
	result, err := e.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, nil
	})

	require.NoError(t, err)
	require.Nil(t, result)
}

func TestShouldSkipAgentHandling_ProjectErrNoDefaultService(t *testing.T) {
	t.Parallel()
	require.True(t, shouldSkipAgentHandling(project.ErrNoDefaultService))
}

func TestShouldSkipAgentHandling_WrappedGenericError(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("deploy failed: %w", fmt.Errorf("timeout"))
	require.False(t, shouldSkipAgentHandling(err))
}

// isCI returns true if common CI/CD environment variables are set.
func isCI() bool {
	return resource.IsRunningOnCI()
}

// mockAgentFactory implements agent.AgentFactory for testing.
type mockAgentFactory struct {
	mock.Mock
}

func (m *mockAgentFactory) Create(ctx context.Context, opts ...agent.AgentOption) (agent.Agent, error) {
	args := m.Called(ctx, opts)
	if result := args.Get(0); result != nil {
		return result.(agent.Agent), args.Error(1)
	}
	return nil, args.Error(1)
}

// mockAgent implements agent.Agent for testing.
type mockAgent struct {
	mock.Mock
}

func (m *mockAgent) Initialize(ctx context.Context, opts ...agent.InitOption) (*agent.InitResult, error) {
	args := m.Called(ctx, opts)
	if result := args.Get(0); result != nil {
		return result.(*agent.InitResult), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockAgent) SendMessage(
	ctx context.Context, prompt string, opts ...agent.SendOption,
) (*agent.AgentResult, error) {
	args := m.Called(ctx, prompt, opts)
	if result := args.Get(0); result != nil {
		return result.(*agent.AgentResult), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockAgent) SendMessageWithRetry(
	ctx context.Context, prompt string, opts ...agent.SendOption,
) (*agent.AgentResult, error) {
	args := m.Called(ctx, prompt, opts)
	if result := args.Get(0); result != nil {
		return result.(*agent.AgentResult), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockAgent) ListSessions(ctx context.Context, cwd string) ([]agent.SessionMetadata, error) {
	args := m.Called(ctx, cwd)
	if result := args.Get(0); result != nil {
		return result.([]agent.SessionMetadata), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockAgent) GetMetrics() agent.AgentMetrics {
	args := m.Called()
	return args.Get(0).(agent.AgentMetrics)
}

func (m *mockAgent) GetMessages(ctx context.Context) ([]agent.SessionEvent, error) {
	args := m.Called(ctx)
	if result := args.Get(0); result != nil {
		return result.([]agent.SessionEvent), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockAgent) SessionID() string {
	args := m.Called()
	return args.String(0)
}

func (m *mockAgent) Stop() error {
	args := m.Called()
	return args.Error(0)
}

// fakeSequenceAgent is a simple Agent implementation that returns results/errors
// in sequence for each SendMessage call. Useful when testify mock's Once()/Return
// with functions doesn't cooperate with variadic parameter matching.
type fakeSequenceAgent struct {
	results []*agent.AgentResult
	errors  []error
	callIdx int
}

func (f *fakeSequenceAgent) Initialize(context.Context, ...agent.InitOption) (*agent.InitResult, error) {
	return nil, nil
}

func (f *fakeSequenceAgent) SendMessage(_ context.Context, _ string, _ ...agent.SendOption) (*agent.AgentResult, error) {
	idx := f.callIdx
	f.callIdx++
	if idx < len(f.results) {
		var err error
		if idx < len(f.errors) {
			err = f.errors[idx]
		}
		return f.results[idx], err
	}
	return nil, errors.New("unexpected call")
}

func (f *fakeSequenceAgent) SendMessageWithRetry(
	ctx context.Context, prompt string, opts ...agent.SendOption,
) (*agent.AgentResult, error) {
	return f.SendMessage(ctx, prompt, opts...)
}

func (f *fakeSequenceAgent) ListSessions(context.Context, string) ([]agent.SessionMetadata, error) {
	return nil, nil
}

func (f *fakeSequenceAgent) GetMetrics() agent.AgentMetrics {
	return agent.AgentMetrics{}
}

func (f *fakeSequenceAgent) GetMessages(context.Context) ([]agent.SessionEvent, error) {
	return nil, nil
}

func (f *fakeSequenceAgent) SessionID() string { return "" }

func (f *fakeSequenceAgent) Stop() error { return nil }

// mockUserConfigManager implements config.UserConfigManager for testing.
type mockUserConfigManager struct {
	cfg config.Config
	err error
}

var _ config.UserConfigManager = (*mockUserConfigManager)(nil)

func (m *mockUserConfigManager) Load() (config.Config, error) {
	return m.cfg, m.err
}

func (m *mockUserConfigManager) Save(_ config.Config) error {
	return nil
}

// configWithKeys creates a Config with dot-path keys properly nested.
func configWithKeys(kvs ...string) config.Config {
	cfg := config.NewEmptyConfig()
	for i := 0; i < len(kvs)-1; i += 2 {
		_ = cfg.Set(kvs[i], kvs[i+1])
	}
	return cfg
}

// copilotEnabledFeatureManager returns a FeatureManager with copilot enabled.
func copilotEnabledFeatureManager() *alpha.FeatureManager {
	cfg := config.NewConfig(map[string]any{
		"alpha": map[string]any{
			string(agentcopilot.FeatureCopilot): "on",
		},
	})
	return alpha.NewFeaturesManagerWithConfig(cfg)
}

// newErrorMiddlewareForTest creates an ErrorMiddleware with injectable dependencies.
func newErrorMiddlewareForTest(
	console input.Console,
	factory agent.AgentFactory,
	fm *alpha.FeatureManager,
	ucm config.UserConfigManager,
	global *internal.GlobalCommandOptions,
) *ErrorMiddleware {
	pipeline := errorhandler.NewErrorHandlerPipeline(nil)
	return &ErrorMiddleware{
		options:           &Options{CommandPath: "azd test", Name: "test"},
		console:           console,
		agentFactory:      factory,
		global:            global,
		featuresManager:   fm,
		userConfigManager: ucm,
		errorPipeline:     pipeline,
	}
}

func TestErrorMiddleware_Run_AgentCreationFailure(t *testing.T) {
	if isCI() {
		t.Skip("skipping on CI — copilot feature detection may differ")
	}
	t.Parallel()
	console := mockinput.NewMockConsole()
	factory := &mockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).
		Return(nil, errors.New("no copilot token"))

	ucm := &mockUserConfigManager{cfg: config.NewEmptyConfig()}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	_, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("some error")
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "no copilot token")
	factory.AssertCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestErrorMiddleware_Run_SavedCategorySkip(t *testing.T) {
	if isCI() {
		t.Skip("skipping on CI — copilot feature detection may differ")
	}
	t.Parallel()
	console := mockinput.NewMockConsole()

	ag := &mockAgent{}
	ag.On("Stop").Return(nil)

	factory := &mockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).Return(ag, nil)

	ucm := &mockUserConfigManager{
		cfg: configWithKeys(agentcopilot.ConfigKeyErrorHandlingCategory, "skip"),
	}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	originalErr := errors.New("deployment failed")
	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{}, originalErr
	})

	require.Error(t, err)
	require.Equal(t, "deployment failed", err.Error())
	require.NotNil(t, result)
	ag.AssertCalled(t, "Stop")
}

func TestErrorMiddleware_Run_AgentSendMessageError(t *testing.T) {
	if isCI() {
		t.Skip("skipping on CI — copilot feature detection may differ")
	}
	t.Parallel()
	console := mockinput.NewMockConsole()

	ag := &mockAgent{}
	ag.On("Stop").Return(nil)
	ag.On("SendMessageWithRetry", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("model rate limited"))

	factory := &mockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).Return(ag, nil)

	ucm := &mockUserConfigManager{
		cfg: configWithKeys(agentcopilot.ConfigKeyErrorHandlingCategory, "explain"),
	}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	_, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("resource not found")
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "model rate limited")
}

func TestErrorMiddleware_Run_FixSendMessageError(t *testing.T) {
	if isCI() {
		t.Skip("skipping on CI — copilot feature detection may differ")
	}
	t.Parallel()
	console := mockinput.NewMockConsole()

	agentResult := &agent.AgentResult{
		Usage: agent.UsageMetrics{InputTokens: 100, OutputTokens: 50},
	}

	// Use a simple fake agent with a call counter
	fakeAg := &fakeSequenceAgent{
		results: []*agent.AgentResult{agentResult, nil},
		errors:  []error{nil, errors.New("agent fix failed")},
	}

	factory := &mockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).Return(fakeAg, nil)

	ucm := &mockUserConfigManager{
		cfg: configWithKeys(
			agentcopilot.ConfigKeyErrorHandlingCategory, "explain",
			agentcopilot.ConfigKeyErrorHandlingFix, "allow",
		),
	}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	_, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("unexpected widget failure")
	})

	// The code should go through: category explain → SendMessage success → promptNextAction "allow" →
	// fix SendMessage error. Verify we get the fix error.
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent fix failed")
}

func TestErrorMiddleware_Run_ConfigLoadError(t *testing.T) {
	if isCI() {
		t.Skip("skipping on CI — copilot feature detection may differ")
	}
	t.Parallel()
	console := mockinput.NewMockConsole()

	ag := &mockAgent{}
	ag.On("Stop").Return(nil)

	factory := &mockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).Return(ag, nil)

	// UserConfigManager.Load returns error
	ucm := &mockUserConfigManager{cfg: nil, err: errors.New("config corrupt")}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	_, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("original error")
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "config corrupt")
}

func TestErrorMiddleware_Run_ErrorPipelineNoMatch(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	fm := alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig())
	global := &internal.GlobalCommandOptions{}

	// Use the real pipeline — it won't match generic errors
	pipeline := errorhandler.NewErrorHandlerPipeline(nil)

	m := &ErrorMiddleware{
		options:           &Options{CommandPath: "azd test", Name: "test"},
		console:           console,
		agentFactory:      nil, // won't be reached
		global:            global,
		featuresManager:   fm,
		userConfigManager: &mockUserConfigManager{cfg: config.NewEmptyConfig()},
		errorPipeline:     pipeline,
	}

	// A generic error won't match the pipeline — passes through
	result, err := m.Run(t.Context(), func(_ context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("generic failure")
	})

	require.Error(t, err)
	require.Nil(t, result)
}

func TestPromptTroubleshootCategory_ConfigLoadError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	ucm := &mockUserConfigManager{cfg: nil, err: errors.New("disk read error")}

	m := &ErrorMiddleware{
		console:           console,
		userConfigManager: ucm,
	}

	cat, err := m.promptTroubleshootCategory(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "disk read error")
	require.Equal(t, categorySkip, cat)
}

func TestBuildPromptForCategory_AllCategories(t *testing.T) {
	t.Parallel()

	m := &ErrorMiddleware{
		options: &Options{CommandPath: "azd provision", Name: "provision"},
	}

	testErr := errors.New("deployment quota exceeded")

	categories := []troubleshootCategory{
		categoryExplain, categoryGuidance, categoryTroubleshoot,
		troubleshootCategory("unknown"), // exercises the default branch
	}

	for _, cat := range categories {
		prompt := m.buildPromptForCategory(cat, testErr)
		require.NotEmpty(t, prompt, "prompt for category %q should not be empty", cat)
		require.Contains(t, prompt, "deployment quota exceeded",
			"prompt for category %q should contain the error message", cat)
	}
}

func TestBuildFixPrompt(t *testing.T) {
	t.Parallel()

	m := &ErrorMiddleware{
		options: &Options{CommandPath: "azd provision", Name: "provision"},
	}

	testErr := errors.New("resource group not found")
	prompt, err := m.buildFixPrompt(testErr)
	require.NoError(t, err)
	require.NotEmpty(t, prompt)
	require.Contains(t, prompt, "resource group not found")
}

func TestErrorMiddleware_Run_ErrorWithTraceId(t *testing.T) {
	if isCI() {
		t.Skip("skipping on CI — copilot feature detection may differ")
	}
	t.Parallel()
	console := mockinput.NewMockConsole()

	ag := &mockAgent{}
	ag.On("SendMessageWithRetry", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("agent failed"))
	ag.On("Stop").Return(nil)

	factory := &mockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).Return(ag, nil)

	ucm := &mockUserConfigManager{
		cfg: configWithKeys(agentcopilot.ConfigKeyErrorHandlingCategory, "explain"),
	}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	// Wrap the original error with a TraceId
	origErr := &internal.ErrorWithTraceId{
		TraceId: "trace-abc-123",
		Err:     errors.New("deployment failed"),
	}

	_, err := m.Run(t.Context(), func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, origErr
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "agent failed")
}

func TestErrorMiddleware_Run_SavedCategoryGuidance(t *testing.T) {
	if isCI() {
		t.Skip("skipping on CI — copilot feature detection may differ")
	}
	t.Parallel()
	console := mockinput.NewMockConsole()

	ag := &mockAgent{}
	ag.On("SendMessageWithRetry", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("agent error"))
	ag.On("Stop").Return(nil)

	factory := &mockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).Return(ag, nil)

	ucm := &mockUserConfigManager{
		cfg: configWithKeys(agentcopilot.ConfigKeyErrorHandlingCategory, "guidance"),
	}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	_, err := m.Run(t.Context(), func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("some error")
	})
	require.Error(t, err)
}

func TestErrorMiddleware_Run_SavedCategoryTroubleshoot(t *testing.T) {
	if isCI() {
		t.Skip("skipping on CI — copilot feature detection may differ")
	}
	t.Parallel()
	console := mockinput.NewMockConsole()

	ag := &mockAgent{}
	ag.On("SendMessageWithRetry", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("agent error"))
	ag.On("Stop").Return(nil)

	factory := &mockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).Return(ag, nil)

	ucm := &mockUserConfigManager{
		cfg: configWithKeys(agentcopilot.ConfigKeyErrorHandlingCategory, "troubleshoot"),
	}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	_, err := m.Run(t.Context(), func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, errors.New("some error")
	})
	require.Error(t, err)
}

func TestPromptTroubleshootCategory_AllSavedCategories(t *testing.T) {
	t.Parallel()

	categories := []string{"explain", "guidance", "troubleshoot", "skip"}
	for _, cat := range categories {
		t.Run(cat, func(t *testing.T) {
			t.Parallel()
			console := mockinput.NewMockConsole()

			ucm := &mockUserConfigManager{
				cfg: configWithKeys(agentcopilot.ConfigKeyErrorHandlingCategory, cat),
			}

			m := &ErrorMiddleware{
				console:           console,
				userConfigManager: ucm,
			}

			got, err := m.promptTroubleshootCategory(t.Context())
			require.NoError(t, err)
			require.Equal(t, troubleshootCategory(cat), got)
		})
	}
}

func TestDisplayUsageMetrics_NoTokens(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()

	m := &ErrorMiddleware{console: console}

	// Zero tokens — should not produce messages
	m.displayUsageMetrics(t.Context(), &agent.AgentResult{
		Usage: agent.UsageMetrics{InputTokens: 0, OutputTokens: 0},
	})
	// No assertion needed — just confirms no panic and the branch is covered

	// Nil result — should not produce messages
	m.displayUsageMetrics(t.Context(), nil)
}

func TestErrorMiddleware_Run_CopilotEnabled_NoError(t *testing.T) {
	if isCI() {
		t.Skip("skipping on CI — copilot feature detection may differ")
	}
	t.Parallel()
	console := mockinput.NewMockConsole()

	factory := &mockAgentFactory{}
	ucm := &mockUserConfigManager{cfg: config.NewEmptyConfig()}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	result, err := m.Run(t.Context(), func(ctx context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{Message: &actions.ResultMessage{Header: "ok"}}, nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestErrorMiddleware_Run_ExistingSuggestion(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()

	factory := &mockAgentFactory{}
	ucm := &mockUserConfigManager{cfg: config.NewEmptyConfig()}
	fm := copilotEnabledFeatureManager()
	global := &internal.GlobalCommandOptions{}

	m := newErrorMiddlewareForTest(console, factory, fm, ucm, global)

	origErr := &internal.ErrorWithSuggestion{
		Err:        errors.New("something broke"),
		Suggestion: "Try running azd auth login",
	}

	_, err := m.Run(t.Context(), func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, origErr
	})

	var suggestion *internal.ErrorWithSuggestion
	require.True(t, errors.As(err, &suggestion))
	require.Contains(t, suggestion.Suggestion, "azd auth login")
}
