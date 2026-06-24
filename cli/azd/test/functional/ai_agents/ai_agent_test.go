// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

package ai_agents_test

import (
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

// --- Tier 0: Offline tests (no Azure, no recording needed) ---

// Test_AIAgent_Version verifies the extension version command works.
func Test_AIAgent_Version(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx, "ai", "agent", "version")
	require.NoError(t, err, "stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	require.Contains(t, result.Stdout, "Version:")
}

// Test_AIAgent_Help verifies the extension help command lists subcommands.
func Test_AIAgent_Help(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx, "ai", "agent", "--help")
	require.NoError(t, err, "stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	require.Contains(t, result.Stdout, "init")
	require.Contains(t, result.Stdout, "invoke")
}

// Test_AIAgent_Init_NoPrompt_MissingFlags verifies --no-prompt without required flags errors clearly.
func Test_AIAgent_Init_NoPrompt_MissingFlags(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	// init --no-prompt should fail with a clear error, not crash.
	// The exact message depends on auth state:
	// - Not logged in → "not logged in" (ensureLoggedIn runs first)
	// - Logged in     → "template selection requires interactive mode"
	result, err := cli.RunCommand(ctx,
		"ai", "agent", "init", "--no-prompt",
		"--deploy-mode", "code",
	)
	require.Error(t, err, "should fail without required flags: stdout=%s", result.Stdout)
	combinedOutput := result.Stdout + result.Stderr
	hasExpectedError := strings.Contains(combinedOutput, "not logged in") ||
		strings.Contains(combinedOutput, "template selection requires interactive mode")
	require.True(t, hasExpectedError,
		"should fail with clear validation error (not logged in OR template selection), got: %s", combinedOutput)
}

// NOTE: SampleList tests moved to ai_agent_recording_test.go (Tier 1) to avoid
// live network dependency — sample list fetches from aka.ms/foundry-agents-samples.

// Test_AIAgent_Doctor_Help verifies doctor --help shows usage.
func Test_AIAgent_Doctor_Help(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx, "ai", "agent", "doctor", "--help")
	require.NoError(t, err, "stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	require.Contains(t, result.Stdout, "doctor")
}
