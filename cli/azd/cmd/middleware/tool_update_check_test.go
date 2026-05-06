// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/tool"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newUpdateCheckMiddleware builds a ToolUpdateCheckMiddleware with the
// given overrides. Fields left nil/zero use safe defaults that cause
// all skip conditions to be met (preventing accidental manager calls).
func newUpdateCheckMiddleware(
	manager *tool.Manager,
	console *mockinput.MockConsole,
	opts *Options,
	globalOpts *internal.GlobalCommandOptions,
) *ToolUpdateCheckMiddleware {
	if opts == nil {
		opts = &Options{CommandPath: "azd provision"}
	}
	if globalOpts == nil {
		globalOpts = &internal.GlobalCommandOptions{}
	}

	return &ToolUpdateCheckMiddleware{
		manager:       manager,
		console:       console,
		options:       opts,
		globalOptions: globalOpts,
	}
}

// ---------------------------------------------------------------------------
// TestToolUpdateCheckMiddleware_SkipNotification
// ---------------------------------------------------------------------------

func TestToolUpdateCheckMiddleware_SkipNotification(t *testing.T) {
	tests := []struct {
		name  string
		setup func(
			t *testing.T,
			console *mockinput.MockConsole,
			opts *Options,
			globalOpts *internal.GlobalCommandOptions,
		)
	}{
		{
			name: "SkipWhenNoPrompt",
			setup: func(
				_ *testing.T,
				_ *mockinput.MockConsole,
				_ *Options,
				g *internal.GlobalCommandOptions,
			) {
				g.NoPrompt = true
			},
		},
		{
			name: "SkipWhenCI",
			setup: func(
				t *testing.T,
				_ *mockinput.MockConsole,
				_ *Options,
				_ *internal.GlobalCommandOptions,
			) {
				t.Setenv("GITHUB_ACTIONS", "true")
			},
		},
		{
			name: "SkipWhenNonInteractive",
			setup: func(
				_ *testing.T,
				console *mockinput.MockConsole,
				_ *Options,
				_ *internal.GlobalCommandOptions,
			) {
				console.SetNoPromptMode(true)
			},
		},
		{
			name: "SkipWhenToolCommand",
			setup: func(
				_ *testing.T,
				_ *mockinput.MockConsole,
				opts *Options,
				_ *internal.GlobalCommandOptions,
			) {
				opts.CommandPath = "azd tool check"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCIVars(t)
			// Ensure triggerBackgroundCheckIfNeeded also skips
			// so the nil manager is never dereferenced.
			t.Setenv(envKeySkipFirstRun, "true")

			console := mockinput.NewMockConsole()
			opts := &Options{CommandPath: "azd provision"}
			globalOpts := &internal.GlobalCommandOptions{}

			tt.setup(t, console, opts, globalOpts)

			m := newUpdateCheckMiddleware(
				nil, // manager not reached in skip path
				console,
				opts,
				globalOpts,
			)

			nextCalled := false
			result, err := m.Run(
				t.Context(),
				passthroughNext(&nextCalled),
			)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.True(t, nextCalled)
			assert.Empty(t, console.Output(),
				"skip path should produce no notification")
		})
	}
}

// ---------------------------------------------------------------------------
// TestToolUpdateCheckMiddleware_ChildAction
// ---------------------------------------------------------------------------

func TestToolUpdateCheckMiddleware_ChildAction(t *testing.T) {
	clearCIVars(t)

	console := mockinput.NewMockConsole()
	m := newUpdateCheckMiddleware(
		nil, console, nil, &internal.GlobalCommandOptions{},
	)

	nextCalled := false
	ctx := WithChildAction(t.Context())
	result, err := m.Run(ctx, passthroughNext(&nextCalled))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, nextCalled)
	assert.Empty(t, console.Output(),
		"child action should skip notification and background check")
}

// ---------------------------------------------------------------------------
// TestToolUpdateCheckMiddleware_NotificationGating
// ---------------------------------------------------------------------------

func TestToolUpdateCheckMiddleware_NotificationGating(t *testing.T) {
	// When shouldSkipNotification returns false the middleware calls
	// manager.ShouldShowNotification. If it returns false, no
	// notification is shown.
	clearCIVars(t)
	t.Setenv(envKeySkipFirstRun, "")
	os.Unsetenv(envKeySkipFirstRun)

	console := mockinput.NewMockConsole()
	cfg := config.NewEmptyConfig()

	// Set a recent lastUpdateCheck so ShouldCheckForUpdates returns
	// false and the background goroutine is never spawned.
	_ = cfg.Set(
		"tool.lastUpdateCheck",
		time.Now().UTC().Format(time.RFC3339),
	)

	ucm := &mockUserConfigManager{cfg: cfg}

	// ShouldShowNotification reads config for lastUpdateCheck.
	// Although the key is set, there is no lastNotificationShown
	// after it, so ShouldShowNotification returns true.  However
	// HasUpdatesAvailable will return (false, 0) because no cache
	// exists → no notification is displayed.
	uc := tool.NewUpdateChecker(ucm, nil, func() (string, error) {
		return t.TempDir(), nil
	}, nil)
	mgr := tool.NewManager(nil, nil, uc)

	m := newUpdateCheckMiddleware(
		mgr,
		console,
		&Options{CommandPath: "azd provision"},
		&internal.GlobalCommandOptions{},
	)

	nextCalled := false
	result, err := m.Run(
		t.Context(),
		passthroughNext(&nextCalled),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, nextCalled)
	assert.Empty(t, console.Output(),
		"ShouldShowNotification=false should suppress notification")
}

// ---------------------------------------------------------------------------
// TestToolUpdateCheckMiddleware_BackgroundCheck
// ---------------------------------------------------------------------------

func TestToolUpdateCheckMiddleware_BackgroundCheckSkippedWhenCI(
	t *testing.T,
) {
	// When CI is detected, triggerBackgroundCheckIfNeeded returns
	// early and never calls manager.ShouldCheckForUpdates.
	clearCIVars(t)
	t.Setenv("CI", "1")

	console := mockinput.NewMockConsole()

	m := newUpdateCheckMiddleware(
		nil, // manager should not be called
		console,
		&Options{CommandPath: "azd provision"},
		&internal.GlobalCommandOptions{},
	)

	nextCalled := false
	result, err := m.Run(
		t.Context(),
		passthroughNext(&nextCalled),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, nextCalled)
}

// ---------------------------------------------------------------------------
// TestToolUpdateCheckMiddleware_BackgroundCheckSkippedWhenEnvVar
// ---------------------------------------------------------------------------

func TestToolUpdateCheckMiddleware_BackgroundCheckSkippedByEnvVar(
	t *testing.T,
) {
	clearCIVars(t)
	t.Setenv(envKeySkipFirstRun, "true")

	console := mockinput.NewMockConsole()

	// globalOptions.NoPrompt causes shouldSkipNotification to return
	// true, so we only exercise the background-check skip path.
	m := newUpdateCheckMiddleware(
		nil,
		console,
		&Options{CommandPath: "azd provision"},
		&internal.GlobalCommandOptions{NoPrompt: true},
	)

	nextCalled := false
	result, err := m.Run(
		t.Context(),
		passthroughNext(&nextCalled),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, nextCalled)
}

// ---------------------------------------------------------------------------
// TestToolUpdateCheckMiddleware_IsToolCommand
// ---------------------------------------------------------------------------

func TestToolUpdateCheckMiddleware_IsToolCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{"azd provision", false},
		{"azd deploy", false},
		{"azd tool", true},
		{"azd tool check", true},
		{"azd tool install", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			m := &ToolUpdateCheckMiddleware{
				options: &Options{CommandPath: tt.path},
			}
			assert.Equal(t, tt.want, m.isToolCommand())
		})
	}
}

// ---------------------------------------------------------------------------
// TestToolUpdateCheckMiddleware_AlwaysCallsNext
// ---------------------------------------------------------------------------

func TestToolUpdateCheckMiddleware_AlwaysCallsNext(t *testing.T) {
	clearCIVars(t)
	// Skip background check so the nil manager is not accessed.
	t.Setenv(envKeySkipFirstRun, "true")

	console := mockinput.NewMockConsole()
	console.SetNoPromptMode(true) // triggers notification skip
	m := newUpdateCheckMiddleware(
		nil, console, nil, &internal.GlobalCommandOptions{},
	)

	expected := &actions.ActionResult{
		Message: &actions.ResultMessage{Header: "hello"},
	}

	result, err := m.Run(
		t.Context(),
		func(_ context.Context) (*actions.ActionResult, error) {
			return expected, nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, expected, result,
		"middleware must propagate the result from nextFn")
}
