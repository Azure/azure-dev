// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/tool"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lookupUsageAttr returns the most recent value set on usage baggage for the
// given key, plus a found flag.  Used by telemetry assertions in this file.
func lookupUsageAttr(key string) (string, bool) {
	for _, a := range tracing.GetUsageAttributes() {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

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
	t.Setenv("AZD_ALPHA_ENABLE_TOOL", "true")

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
				alphaManager:  alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
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
	t.Setenv("AZD_ALPHA_ENABLE_TOOL", "true")

	console := mockinput.NewMockConsole()
	cfg := config.NewEmptyConfig()
	opts := &internal.GlobalCommandOptions{}
	ucm := &mockUserConfigManager{cfg: cfg}

	m := &ToolFirstRunMiddleware{
		alphaManager:  alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
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

// TestToolFirstRunMiddleware_SkipsWhenAlphaDisabled verifies that the
// middleware is a no-op when the tool alpha feature is not enabled.
func TestToolFirstRunMiddleware_SkipsWhenAlphaDisabled(t *testing.T) {
	clearCIVars(t)
	t.Setenv(envKeySkipFirstRun, "")
	os.Unsetenv(envKeySkipFirstRun)
	t.Setenv("AZD_ALPHA_ENABLE_TOOL", "false")

	console := mockinput.NewMockConsole()
	cfg := config.NewEmptyConfig()
	opts := &internal.GlobalCommandOptions{}
	ucm := &mockUserConfigManager{cfg: cfg}

	m := &ToolFirstRunMiddleware{
		alphaManager:  alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		configManager: ucm,
		console:       console,
		manager:       nil,
		options:       opts,
	}

	nextCalled := false
	result, err := m.Run(t.Context(), passthroughNext(&nextCalled))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, nextCalled,
		"nextFn must always be called even when alpha is disabled")
	assert.Empty(t, console.Output(),
		"alpha-disabled should skip the first-run experience entirely")
}

// TestToolFirstRunMiddleware_EmitsSkipReason verifies that each documented
// skip path sets the tool.firstrun.skip_reason usage attribute to the
// expected value.
func TestToolFirstRunMiddleware_EmitsSkipReason(t *testing.T) {
	t.Setenv("AZD_ALPHA_ENABLE_TOOL", "true")

	tests := []struct {
		name       string
		setup      func(t *testing.T, console *mockinput.MockConsole, cfg config.Config, opts *internal.GlobalCommandOptions)
		wantReason string
	}{
		{
			name: "env_var",
			setup: func(t *testing.T, _ *mockinput.MockConsole, _ config.Config, _ *internal.GlobalCommandOptions) {
				t.Setenv(envKeySkipFirstRun, "true")
			},
			wantReason: skipReasonEnvVar,
		},
		{
			name: "no_prompt",
			setup: func(_ *testing.T, _ *mockinput.MockConsole, _ config.Config, opts *internal.GlobalCommandOptions) {
				opts.NoPrompt = true
			},
			wantReason: skipReasonNoPrompt,
		},
		{
			name: "ci_cd",
			setup: func(t *testing.T, _ *mockinput.MockConsole, _ config.Config, _ *internal.GlobalCommandOptions) {
				t.Setenv("GITHUB_ACTIONS", "true")
			},
			wantReason: skipReasonCICD,
		},
		{
			name: "non_interactive",
			setup: func(_ *testing.T, console *mockinput.MockConsole, _ config.Config, _ *internal.GlobalCommandOptions) {
				console.SetNoPromptMode(true)
			},
			wantReason: skipReasonNonInteractive,
		},
		{
			name: "already_completed",
			setup: func(_ *testing.T, _ *mockinput.MockConsole, cfg config.Config, _ *internal.GlobalCommandOptions) {
				_ = cfg.Set(configKeyFirstRunCompleted, "2024-01-01T00:00:00Z")
			},
			wantReason: skipReasonAlreadyCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCIVars(t)
			t.Setenv(envKeySkipFirstRun, "")
			os.Unsetenv(envKeySkipFirstRun)

			// Reset the skip_reason attribute to a sentinel so we can
			// verify the middleware overwrote it during this test.
			tracing.SetUsageAttributes(fields.ToolFirstRunSkipReasonKey.String("__unset__"))

			console := mockinput.NewMockConsole()
			cfg := config.NewEmptyConfig()
			opts := &internal.GlobalCommandOptions{}
			tt.setup(t, console, cfg, opts)

			ucm := &mockUserConfigManager{cfg: cfg}
			m := &ToolFirstRunMiddleware{
				alphaManager:  alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
				configManager: ucm,
				console:       console,
				manager:       nil,
				options:       opts,
			}

			nextCalled := false
			_, err := m.Run(t.Context(), passthroughNext(&nextCalled))
			require.NoError(t, err)
			assert.True(t, nextCalled)

			got, ok := lookupUsageAttr(string(fields.ToolFirstRunSkipReasonKey.Key))
			require.True(t, ok, "expected tool.firstrun.skip_reason to be set")
			assert.Equal(t, tt.wantReason, got)
		})
	}
}

// TestToolFirstRunMiddleware_NoSkipReasonForSilentPaths verifies that the
// alpha-disabled and child-action skip paths do NOT emit
// tool.firstrun.skip_reason (these paths are intentionally silent because the
// user has no opportunity to opt in / out).
func TestToolFirstRunMiddleware_NoSkipReasonForSilentPaths(t *testing.T) {
	clearCIVars(t)
	t.Setenv(envKeySkipFirstRun, "")
	os.Unsetenv(envKeySkipFirstRun)

	cases := []struct {
		name     string
		alphaOn  bool
		childCtx bool
	}{
		{name: "alpha_disabled", alphaOn: false},
		{name: "child_action", alphaOn: true, childCtx: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.alphaOn {
				t.Setenv("AZD_ALPHA_ENABLE_TOOL", "true")
			} else {
				t.Setenv("AZD_ALPHA_ENABLE_TOOL", "false")
			}

			sentinel := "__silent_" + tc.name + "__"
			tracing.SetUsageAttributes(fields.ToolFirstRunSkipReasonKey.String(sentinel))

			console := mockinput.NewMockConsole()
			cfg := config.NewEmptyConfig()
			opts := &internal.GlobalCommandOptions{}
			ucm := &mockUserConfigManager{cfg: cfg}

			m := &ToolFirstRunMiddleware{
				alphaManager:  alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
				configManager: ucm,
				console:       console,
				manager:       nil,
				options:       opts,
			}

			ctx := t.Context()
			if tc.childCtx {
				ctx = WithChildAction(ctx)
			}

			nextCalled := false
			_, err := m.Run(ctx, passthroughNext(&nextCalled))
			require.NoError(t, err)
			assert.True(t, nextCalled)

			// Because alpha-disabled / child-action paths are silent, the
			// sentinel we wrote before invocation must still be the value.
			got, ok := lookupUsageAttr(string(fields.ToolFirstRunSkipReasonKey.Key))
			require.True(t, ok)
			assert.Equal(t, sentinel, got,
				"silent skip path must not overwrite tool.firstrun.skip_reason")
		})
	}
}

// ---------------------------------------------------------------------------
// markCompleted
// ---------------------------------------------------------------------------

// TestToolFirstRunMiddleware_MarkCompleted_PersistsKey verifies that
// markCompleted writes the documented `tool.firstRunCompleted` key into
// user config so the experience is suppressed on subsequent runs.
func TestToolFirstRunMiddleware_MarkCompleted_PersistsKey(t *testing.T) {
	cfg := config.NewEmptyConfig()
	ucm := &mockUserConfigManager{cfg: cfg}

	m := &ToolFirstRunMiddleware{
		configManager: ucm,
	}

	m.markCompleted()

	got, ok := cfg.Get(configKeyFirstRunCompleted)
	require.True(t, ok, "markCompleted must persist the firstRunCompleted key")
	require.NotEmpty(t, got, "persisted value should be a non-empty timestamp")
}

// TestToolFirstRunMiddleware_MarkCompleted_LoadError verifies that a
// failure to load user config is logged and swallowed — markCompleted
// must never panic or escalate the error, because the first-run flow
// is best-effort.
func TestToolFirstRunMiddleware_MarkCompleted_LoadError(t *testing.T) {
	ucm := &mockUserConfigManager{
		cfg: config.NewEmptyConfig(),
		err: assert.AnError,
	}

	m := &ToolFirstRunMiddleware{
		configManager: ucm,
	}

	// Should not panic; should silently swallow the load error.
	require.NotPanics(t, func() {
		m.markCompleted()
	})
}

// ---------------------------------------------------------------------------
// displayToolStatuses
// ---------------------------------------------------------------------------

// TestToolFirstRunMiddleware_DisplayToolStatuses verifies that each
// tool produces a status line — installed tools show their version,
// missing tools render the "not installed" marker, and entries with a
// nil Tool reference are skipped without panicking.
func TestToolFirstRunMiddleware_DisplayToolStatuses(t *testing.T) {
	console := mockinput.NewMockConsole()
	m := &ToolFirstRunMiddleware{console: console}

	statuses := []*tool.ToolStatus{
		{
			Tool:             &tool.ToolDefinition{Id: "az-cli", Name: "Azure CLI"},
			Installed:        true,
			InstalledVersion: "2.73.0",
		},
		{
			Tool:      &tool.ToolDefinition{Id: "kubectl", Name: "kubectl"},
			Installed: true, // missing version → renders "installed"
		},
		{
			Tool:      &tool.ToolDefinition{Id: "helm", Name: "Helm"},
			Installed: false,
		},
		{Tool: nil}, // nil Tool must be skipped silently
	}

	require.NotPanics(t, func() {
		m.displayToolStatuses(context.Background(), statuses)
	})

	output := console.Output()
	joined := ""
	for _, line := range output {
		joined += line + "\n"
	}
	assert.Contains(t, joined, "Azure CLI")
	assert.Contains(t, joined, "2.73.0")
	assert.Contains(t, joined, "kubectl")
	assert.Contains(t, joined, "installed")
	assert.Contains(t, joined, "Helm")
	assert.Contains(t, joined, "not installed")
}
