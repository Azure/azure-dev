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
	"github.com/azure/azure-dev/cli/azd/internal/agent"
	agentcopilot "github.com/azure/azure-dev/cli/azd/internal/agent/copilot"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/pack"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_ErrorMiddleware_SuccessNoError(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
		wrapped := fmt.Errorf("preflight declined: %w", internal.ErrAbortedByUser)
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

	mockContext := mocks.NewMockContext(context.Background())
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

func Test_PromptNextAction_SavedAllow_ReturnsFixOnly(t *testing.T) {
	t.Parallel()

	cfg := configWithKeys(agentcopilot.ConfigKeyErrorHandlingFix, "allow")
	ucm := &mockUserConfigManager{cfg: cfg}

	m := &ErrorMiddleware{
		console:           mockinput.NewMockConsole(),
		userConfigManager: ucm,
	}

	action, err := m.promptNextAction(t.Context())
	require.NoError(t, err)
	require.Equal(t, actionFixOnly, action,
		"saved 'allow' preference should return actionFixOnly, not actionFixAndRetry")
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
