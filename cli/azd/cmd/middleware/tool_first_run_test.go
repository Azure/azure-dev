// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// clearCIVars unsets every CI-related env var so that
// resource.IsRunningOnCI() returns false during the test.
func clearCIVars(t *testing.T) {
	t.Helper()

	ciVars := []string{
		"TF_BUILD", "GITHUB_ACTIONS", "APPVEYOR",
		"TRAVIS", "CIRCLECI", "GITLAB_CI",
		"CODEBUILD_BUILD_ID", "JENKINS_URL",
		"TEAMCITY_VERSION", "JB_SPACE_API_URL",
		"bamboo.buildKey", "BITBUCKET_BUILD_NUMBER",
		"CI", "BUILD_ID",
	}

	for _, v := range ciVars {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
}

// passthroughNext returns a NextFn that records whether it was called
// and returns a dummy success result.
func passthroughNext(called *bool) NextFn {
	return func(_ context.Context) (*actions.ActionResult, error) {
		*called = true
		return &actions.ActionResult{
			Message: &actions.ResultMessage{Header: "OK"},
		}, nil
	}
}

// ---------------------------------------------------------------------------
// TestToolFirstRunMiddleware_SkipConditions
// ---------------------------------------------------------------------------

func TestToolFirstRunMiddleware_SkipConditions(t *testing.T) {
	// These tests modify env vars via t.Setenv so they cannot be
	// parallel with each other or with tests that read the same vars.

	tests := []struct {
		name     string
		setup    func(t *testing.T, console *mockinput.MockConsole, cfg config.Config, opts *internal.GlobalCommandOptions)
		childCtx bool // wrap context with WithChildAction
		wantSkip bool
	}{
		{
			name: "SkipWhenEnvVarSet",
			setup: func(t *testing.T, _ *mockinput.MockConsole, _ config.Config, _ *internal.GlobalCommandOptions) {
				t.Setenv(envKeySkipFirstRun, "true")
			},
			wantSkip: true,
		},
		{
			name: "SkipWhenNoPrompt",
			setup: func(
				_ *testing.T,
				_ *mockinput.MockConsole,
				_ config.Config,
				opts *internal.GlobalCommandOptions,
			) {
				opts.NoPrompt = true
			},
			wantSkip: true,
		},
		{
			name: "SkipWhenCI",
			setup: func(t *testing.T, _ *mockinput.MockConsole, _ config.Config, _ *internal.GlobalCommandOptions) {
				t.Setenv("GITHUB_ACTIONS", "true")
			},
			wantSkip: true,
		},
		{
			name: "SkipWhenNonInteractive",
			setup: func(
				_ *testing.T,
				console *mockinput.MockConsole,
				_ config.Config,
				_ *internal.GlobalCommandOptions,
			) {
				console.SetNoPromptMode(true)
			},
			wantSkip: true,
		},
		{
			name: "SkipWhenAlreadyCompleted",
			setup: func(
				_ *testing.T,
				_ *mockinput.MockConsole,
				cfg config.Config,
				_ *internal.GlobalCommandOptions,
			) {
				_ = cfg.Set(
					configKeyFirstRunCompleted,
					"2024-01-01T00:00:00Z",
				)
			},
			wantSkip: true,
		},
		{
			name:     "SkipWhenChildAction",
			setup:    func(_ *testing.T, _ *mockinput.MockConsole, _ config.Config, _ *internal.GlobalCommandOptions) {},
			childCtx: true,
			wantSkip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCIVars(t)
			t.Setenv(envKeySkipFirstRun, "")
			os.Unsetenv(envKeySkipFirstRun)

			console := mockinput.NewMockConsole()
			cfg := config.NewEmptyConfig()
			opts := &internal.GlobalCommandOptions{}

			tt.setup(t, console, cfg, opts)

			ucm := &mockUserConfigManager{cfg: cfg}

			m := &ToolFirstRunMiddleware{
				configManager: ucm,
				console:       console,
				manager:       nil, // not needed for skip paths
				options:       opts,
			}

			ctx := t.Context()
			if tt.childCtx {
				ctx = WithChildAction(ctx)
			}

			nextCalled := false
			result, err := m.Run(ctx, passthroughNext(&nextCalled))

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.True(t, nextCalled,
				"nextFn must always be called")

			if tt.wantSkip {
				assert.Empty(t, console.Output(),
					"skip path should produce no console output")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestToolFirstRunMiddleware_TriggersWhenNoSkip
// ---------------------------------------------------------------------------

func TestToolFirstRunMiddleware_TriggersWhenNoSkip(t *testing.T) {
	// Ensure no CI env vars interfere.
	clearCIVars(t)
	t.Setenv(envKeySkipFirstRun, "")
	os.Unsetenv(envKeySkipFirstRun)

	console := mockinput.NewMockConsole()
	cfg := config.NewEmptyConfig()
	opts := &internal.GlobalCommandOptions{}
	ucm := &mockUserConfigManager{cfg: cfg}

	m := &ToolFirstRunMiddleware{
		configManager: ucm,
		console:       console,
		manager:       nil, // runFirstRunExperience will fail at prompt
		options:       opts,
	}

	nextCalled := false
	result, err := m.Run(t.Context(), passthroughNext(&nextCalled))

	// The middleware must never block the user's command, even if
	// the first-run experience itself fails.
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, nextCalled,
		"nextFn must be called even when the first-run experience fails")

	// The welcome banner is printed before any interactive prompt,
	// so it should appear in the console output.
	assert.NotEmpty(t, console.Output(),
		"triggered first-run experience should produce console output")
}
