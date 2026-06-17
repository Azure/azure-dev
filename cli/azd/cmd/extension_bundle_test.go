// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestIsBundleArg(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bundle.zip")
	require.NoError(t, os.WriteFile(zipPath, []byte("zip"), 0600))

	require.True(t, isBundleArg([]string{zipPath}))
	require.False(t, isBundleArg([]string{filepath.Join(dir, "missing.zip")})) // does not exist
	require.False(t, isBundleArg([]string{"some.extension.id"}))
	require.False(t, isBundleArg([]string{zipPath, "other"}))
	require.False(t, isBundleArg([]string{dir})) // directory, not a file
	require.False(t, isBundleArg(nil))
}

func TestNormalizeBundleSourceName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"My Bundle":            "my-bundle",
		"ext_1.0.0":            "ext_1-0-0",
		"weird@@name!!":        "weird-name",
		"--leading-trailing--": "leading-trailing",
		"UPPER":                "upper",
	}

	for input, expected := range cases {
		require.Equal(t, expected, normalizeBundleSourceName(input), "input %q", input)
	}
}

func TestBundleSourceName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "my-ext_1-0-0", bundleSourceName("/tmp/My Ext_1.0.0.zip"))
	require.Equal(t, "bundle", bundleSourceName("bundle.zip"))
}

func TestCleanupBundleInstall_RemovesTempDir(t *testing.T) {
	t.Parallel()

	bundleDir := t.TempDir()
	marker := filepath.Join(bundleDir, "registry.json")
	require.NoError(t, os.WriteFile(marker, []byte("{}"), 0600))

	// With no transient source recorded, cleanup only removes the temp dir and
	// does not touch the (nil) managers.
	action := &extensionInstallAction{bundleTempDir: bundleDir}
	action.cleanupBundleInstall(context.Background())

	require.NoDirExists(t, bundleDir)
	require.Empty(t, action.bundleTempDir)

	// Idempotent: a second call is a no-op and does not panic.
	require.NotPanics(t, func() {
		action.cleanupBundleInstall(context.Background())
	})
}

func TestRandomHexToken(t *testing.T) {
	t.Parallel()

	a, err := randomHexToken()
	require.NoError(t, err)
	require.Len(t, a, 8)

	b, err := randomHexToken()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestSourceDisplayLabel(t *testing.T) {
	t.Parallel()

	// During a bundle install the transient source name is presented as "bundle".
	withBundle := &extensionInstallAction{bundleSourceName: "demo-abc123"}
	require.Equal(t, "bundle", withBundle.sourceDisplayLabel("demo-abc123"))
	require.Equal(t, "bundle", withBundle.sourceDisplayLabel("DEMO-ABC123"))

	// The reserved bundle source is also presented as "bundle".
	require.Equal(t, "bundle", withBundle.sourceDisplayLabel(extensions.BundleSourceName))

	// Regular sources are quoted by name.
	require.Equal(t, `source "azd"`, withBundle.sourceDisplayLabel("azd"))

	noBundle := &extensionInstallAction{}
	require.Equal(t, "bundle", noBundle.sourceDisplayLabel(extensions.BundleSourceName))
	require.Equal(t, `source "local"`, noBundle.sourceDisplayLabel("local"))
}

func TestWrapDependencyError(t *testing.T) {
	t.Parallel()

	// Non-dependency errors pass through unchanged.
	plain := fmt.Errorf("some other failure")
	require.Equal(t, plain, wrapDependencyError(plain))

	// Dependency-not-found errors are wrapped with an actionable suggestion.
	depErr := fmt.Errorf("install failed: %w", &extensions.DependencyNotFoundError{
		DependencyId: "azure.ai.inspector",
		ParentId:     "azure.ai.agents",
	})
	wrapped := wrapDependencyError(depErr)

	suggestErr, ok := errors.AsType[*internal.ErrorWithSuggestion](wrapped)
	require.True(t, ok, "expected ErrorWithSuggestion, got %T", wrapped)
	require.Contains(t, suggestErr.Suggestion, "azd extension install azure.ai.inspector")
	require.ErrorAs(t, suggestErr.Err, new(*extensions.DependencyNotFoundError))
}

func newConfirmTestAction(console input.Console, noPrompt bool) *extensionInstallAction {
	return &extensionInstallAction{
		console: console,
		flags: &extensionInstallFlags{
			global: &internal.GlobalCommandOptions{NoPrompt: noPrompt},
		},
	}
}

func TestConfirmSourceChange(t *testing.T) {
	t.Parallel()

	installed := &extensions.Extension{
		Id:      "azure.ai.agents",
		Version: "1.0.0",
		Source:  "azd",
	}

	t.Run("ReinstallSameVersion", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmSourceChange(
			context.Background(), "Installing", installed.Id, installed, extensions.BundleSourceName, "1.0.0",
		)
		require.NoError(t, err)
		require.True(t, proceed)
		require.Contains(t, lastConfirmMessage(console),
			`azure.ai.agents 1.0.0 is installed from source "azd". Reinstall from bundle?`)
	})

	t.Run("UpgradeShowsTargetVersion", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmSourceChange(
			context.Background(), "Installing", installed.Id, installed, extensions.BundleSourceName, "2.0.0",
		)
		require.NoError(t, err)
		require.True(t, proceed)
		require.Contains(t, lastConfirmMessage(console),
			`azure.ai.agents 1.0.0 is installed from source "azd". Upgrade to 2.0.0 from bundle?`)
	})

	t.Run("DowngradeDeclined", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(false)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmSourceChange(
			context.Background(), "Installing", installed.Id, installed, extensions.BundleSourceName, "0.9.0",
		)
		require.NoError(t, err)
		require.False(t, proceed)
		require.Contains(t, lastConfirmMessage(console),
			`azure.ai.agents 1.0.0 is installed from source "azd". Downgrade to 0.9.0 from bundle?`)
	})

	t.Run("NoPromptSkipsWithoutAsking", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		// No WhenConfirm registered: if Confirm were called it would panic/fail.
		action := newConfirmTestAction(console, true)

		proceed, err := action.confirmSourceChange(
			context.Background(), "Installing", installed.Id, installed, extensions.BundleSourceName, "1.0.0",
		)
		require.NoError(t, err)
		require.False(t, proceed)
	})
}

// lastConfirmMessage returns the most recent non-empty message the mock console
// captured, which for a confirm prompt is the prompt text (ignoring blank-line
// spacing messages).
func lastConfirmMessage(console *mockinput.MockConsole) string {
	out := console.Output()
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] != "" {
			return out[i]
		}
	}
	return ""
}

func TestConfirmReplace(t *testing.T) {
	t.Parallel()

	const question = "azure.ai.agents version 1.0.0 is installed. Downgrade to 0.9.0?"
	const skipSuffix = " (would downgrade from 1.0.0 to 0.9.0, use --force to override)"

	t.Run("Confirmed", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmReplace(context.Background(), "Installing", question, skipSuffix)
		require.NoError(t, err)
		require.True(t, proceed)
		require.Contains(t, lastConfirmMessage(console), question)
	})

	t.Run("Declined", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(false)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmReplace(context.Background(), "Installing", question, skipSuffix)
		require.NoError(t, err)
		require.False(t, proceed)
	})

	t.Run("NoPromptSkipsWithoutAsking", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		// No WhenConfirm registered: if Confirm were called it would fail.
		action := newConfirmTestAction(console, true)

		proceed, err := action.confirmReplace(context.Background(), "Installing", question, skipSuffix)
		require.NoError(t, err)
		require.False(t, proceed)
	})
}

func TestVersionTransitionVerb(t *testing.T) {
	t.Parallel()

	cases := []struct {
		installed string
		target    string
		expected  string
	}{
		{"1.0.0", "1.0.0", "Reinstall"},
		{"1.0.0", "2.0.0", "Upgrade to 2.0.0"},
		{"1.0.0", "0.9.0", "Downgrade to 0.9.0"},
		{"1.0.0-preview", "1.0.0", "Upgrade to 1.0.0"},
		// Non-semver tags have no defined ordering -> neutral verb.
		{"nightly", "1.0.0", "Replace with 1.0.0"},
		{"1.0.0", "nightly", "Replace with nightly"},
	}

	for _, tc := range cases {
		require.Equal(t, tc.expected, versionTransitionVerb(tc.installed, tc.target),
			"installed=%s target=%s", tc.installed, tc.target)
	}
}
