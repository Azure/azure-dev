// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

package cli_test

import (
	"encoding/json"
	"os"
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

// Test_AIAgent_Init_NoPrompt_MissingFlags verifies --no-prompt without required flags errors.
func Test_AIAgent_Init_NoPrompt_MissingFlags(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	// Missing -m flag should fail
	result, err := cli.RunCommand(ctx,
		"ai", "agent", "init", "--no-prompt",
		"--deploy-mode", "code",
	)
	require.Error(t, err, "should fail without -m flag: stdout=%s", result.Stdout)
	combinedOutput := result.Stdout + result.Stderr
	require.Contains(t, combinedOutput, "template selection requires interactive mode",
		"should fail with clear validation error, not crash")
}

// Test_AIAgent_SampleList verifies sample list returns results.
func Test_AIAgent_SampleList(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx, "ai", "agent", "sample", "list")
	require.NoError(t, err, "stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	require.Greater(t, len(result.Stdout), 50, "sample list output too short")
}

// Test_AIAgent_SampleList_JSON verifies sample list --output json returns valid JSON array.
func Test_AIAgent_SampleList_JSON(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx, "ai", "agent", "sample", "list", "--output", "json")
	require.NoError(t, err, "stdout=%s, stderr=%s", result.Stdout, result.Stderr)

	var output map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &output), "output is not valid JSON: %s", result.Stdout)
	require.Contains(t, output, "templates", "expected 'templates' key in JSON output")
}

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
