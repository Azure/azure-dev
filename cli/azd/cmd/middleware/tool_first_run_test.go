// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
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
					"true",
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

// TestToolFirstRunMiddleware_EmitsSkipReason verifies that each documented
// skip path sets the tool.firstrun.skip_reason usage attribute to the
// expected value.
func TestToolFirstRunMiddleware_EmitsSkipReason(t *testing.T) {
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
				_ = cfg.Set(configKeyFirstRunCompleted, "true")
			},
			wantReason: skipReasonAlreadyCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCIVars(t)
			t.Setenv(envKeySkipFirstRun, "")
			os.Unsetenv(envKeySkipFirstRun)

			// Reset usage attributes so we can verify the middleware
			// affirmatively writes tool.firstrun.skip_reason in this path.
			tracing.ResetUsageAttributesForTest()

			console := mockinput.NewMockConsole()
			cfg := config.NewEmptyConfig()
			opts := &internal.GlobalCommandOptions{}
			tt.setup(t, console, cfg, opts)

			ucm := &mockUserConfigManager{cfg: cfg}
			m := &ToolFirstRunMiddleware{
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

// TestToolFirstRunMiddleware_NoSkipReasonForChildAction verifies that the
// child-action skip path does NOT emit tool.firstrun.skip_reason — child
// actions inherit their parent's first-run state and shouldn't contribute
// to first-run adoption analysis.
func TestToolFirstRunMiddleware_NoSkipReasonForChildAction(t *testing.T) {
	clearCIVars(t)
	t.Setenv(envKeySkipFirstRun, "")
	os.Unsetenv(envKeySkipFirstRun)

	tracing.ResetUsageAttributesForTest()

	console := mockinput.NewMockConsole()
	cfg := config.NewEmptyConfig()
	opts := &internal.GlobalCommandOptions{}
	ucm := &mockUserConfigManager{cfg: cfg}

	m := &ToolFirstRunMiddleware{
		configManager: ucm,
		console:       console,
		manager:       nil,
		options:       opts,
	}

	ctx := WithChildAction(t.Context())

	nextCalled := false
	_, err := m.Run(ctx, passthroughNext(&nextCalled))
	require.NoError(t, err)
	assert.True(t, nextCalled)

	// Child-action skip path must not emit tool.firstrun.skip_reason —
	// child actions inherit the parent's first-run state, so there's
	// nothing meaningful to record.
	_, ok := lookupUsageAttr(string(fields.ToolFirstRunSkipReasonKey.Key))
	assert.False(t, ok,
		"child-action skip path must not emit tool.firstrun.skip_reason")
}

// TestToolFirstRunMiddleware_ShouldSkip_FirstRunCompletedValue verifies
// that the `tool.firstRunCompleted` value is interpreted via
// strconv.ParseBool. Only a parseable truthy value suppresses the
// first-run prompt; anything else (including falsy values, empty
// string, and unparseable strings) lets it run, so users can re-enable
// the prompt with `azd config set tool.firstRunCompleted false`
// instead of `azd config unset`.
func TestToolFirstRunMiddleware_ShouldSkip_FirstRunCompletedValue(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		wantSkip bool
	}{
		{name: "explicit_true", value: "true", wantSkip: true},
		{name: "explicit_one", value: "1", wantSkip: true},
		{name: "explicit_false", value: "false", wantSkip: false},
		{name: "explicit_zero", value: "0", wantSkip: false},
		{name: "empty_string", value: "", wantSkip: false},
		{name: "rfc3339_timestamp", value: "2024-01-01T00:00:00Z", wantSkip: true},
		{name: "garbage_string", value: "not-a-bool-or-timestamp", wantSkip: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCIVars(t)
			t.Setenv(envKeySkipFirstRun, "")
			os.Unsetenv(envKeySkipFirstRun)

			cfg := config.NewEmptyConfig()
			require.NoError(t, cfg.Set(configKeyFirstRunCompleted, tt.value))

			m := &ToolFirstRunMiddleware{
				configManager: &mockUserConfigManager{cfg: cfg},
				console:       mockinput.NewMockConsole(),
				options:       &internal.GlobalCommandOptions{},
			}

			_, skip := m.shouldSkip(t.Context())
			assert.Equal(t, tt.wantSkip, skip)
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
	var sb strings.Builder
	for _, line := range output {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	joined := sb.String()
	assert.Contains(t, joined, "Azure CLI")
	assert.Contains(t, joined, "2.73.0")
	assert.Contains(t, joined, "Helm")
	assert.Contains(t, joined, "not installed")

	// The kubectl status has Installed=true but no InstalledVersion; the
	// renderer must produce a kubectl-specific line containing "installed"
	// (without the "not " prefix). A bare assert.Contains(joined, "installed")
	// would pass on az-cli's "installed 2.73.0" line alone, so locate the
	// kubectl line explicitly.
	var kubectlLine string
	for _, line := range output {
		if strings.Contains(line, "kubectl") {
			kubectlLine = line
			break
		}
	}
	require.NotEmpty(t, kubectlLine, "kubectl status line must be rendered")
	assert.Contains(t, kubectlLine, "installed")
	assert.NotContains(t, kubectlLine, "not installed")
}

// ---------------------------------------------------------------------------
// NewToolFirstRunMiddleware constructor
// ---------------------------------------------------------------------------

// TestNewToolFirstRunMiddleware verifies the public constructor populates
// every dependency on the returned middleware. This guards against future
// refactors that silently drop a field assignment.
func TestNewToolFirstRunMiddleware(t *testing.T) {
	console := mockinput.NewMockConsole()
	ucm := &mockUserConfigManager{cfg: config.NewEmptyConfig()}
	opts := &internal.GlobalCommandOptions{}

	mw := NewToolFirstRunMiddleware(ucm, console, nil, opts)
	require.NotNil(t, mw)

	concrete, ok := mw.(*ToolFirstRunMiddleware)
	require.True(t, ok, "NewToolFirstRunMiddleware must return a *ToolFirstRunMiddleware")
	assert.Same(t, ucm, concrete.configManager)
	assert.Same(t, console, concrete.console)
	assert.Nil(t, concrete.manager)
	assert.Same(t, opts, concrete.options)
}

// ---------------------------------------------------------------------------
// offerInstall — interactive error path
// ---------------------------------------------------------------------------

// TestToolFirstRunMiddleware_OfferInstall_PromptError exercises offerInstall
// with an empty stdin so the underlying multi-select prompt fails on read.
// This covers attribute emission (tools_offered), prompt setup, and error
// handling without requiring a real terminal or an installable Manager.
func TestToolFirstRunMiddleware_OfferInstall_PromptError(t *testing.T) {
	tracing.ResetUsageAttributesForTest()
	t.Cleanup(tracing.ResetUsageAttributesForTest)

	console := mockinput.NewMockConsole()
	m := &ToolFirstRunMiddleware{
		console: console,
		manager: nil, // multi-select fails before manager is invoked
	}

	missing := []*tool.ToolStatus{
		{
			Tool: &tool.ToolDefinition{
				Id:          "az-cli",
				Name:        "Azure CLI",
				Description: "Microsoft Azure CLI",
				Priority:    tool.ToolPriorityRecommended,
			},
			Installed: false,
		},
		{
			Tool: &tool.ToolDefinition{
				Id:          "github-copilot-cli",
				Name:        "GitHub Copilot CLI",
				Description: "GitHub Copilot in the terminal",
				Priority:    tool.ToolPriorityRecommended,
			},
			Installed: false,
		},
	}

	// Empty stdin from MockConsole.Handles() forces multi-select to fail
	// on read. Whether that surfaces as ErrCancelled (outcomeCancelled
	// branch) or a wrapped error, offerInstall must not panic and must
	// emit the tools_offered count.
	outcome, err := m.offerInstall(t.Context(), missing)
	_ = outcome
	_ = err

	var offered int64
	var found bool
	for _, a := range tracing.GetUsageAttributes() {
		if a.Key == fields.ToolFirstRunToolsOfferedKey.Key {
			offered = a.Value.AsInt64()
			found = true
			break
		}
	}
	require.True(t, found, "offerInstall must emit tool.firstrun.tools_offered")
	assert.Equal(t, int64(2), offered, "tools_offered must reflect missing count")
}
