// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

// Test_CLI_PromptTimeout verifies the prompt timeout feature works correctly.
// Tests use short timeouts (≤2s) to minimize test execution time.
func Test_CLI_PromptTimeout(t *testing.T) {
	t.Parallel()

	t.Run("env var recognized", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		testCtx, testCancel := context.WithTimeout(ctx, 2*time.Second)
		defer testCancel()

		dir := tempDirWithDiagnostics(t)
		cli := azdcli.NewCLI(t)
		cli.WorkingDirectory = dir
		cli.Env = append(os.Environ(), input.PromptTimeoutEnvVar+"=1")

		result, err := cli.RunCommandWithStdIn(testCtx, "", "init")

		require.Error(t, err)
		require.NotNil(t, result)
	})

	t.Run("zero disables timeout", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		testCtx, testCancel := context.WithTimeout(ctx, 2*time.Second)
		defer testCancel()

		dir := tempDirWithDiagnostics(t)
		cli := azdcli.NewCLI(t)
		cli.WorkingDirectory = dir
		cli.Env = append(os.Environ(), input.PromptTimeoutEnvVar+"=0")

		result, err := cli.RunCommandWithStdIn(testCtx, "", "init")

		require.Error(t, err)
		require.NotNil(t, result)
		output := result.Stdout + result.Stderr
		require.False(t, strings.Contains(output, "prompt timed out"),
			"timeout disabled, should not see timeout message")
	})

	t.Run("negative disables timeout", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		testCtx, testCancel := context.WithTimeout(ctx, 2*time.Second)
		defer testCancel()

		dir := tempDirWithDiagnostics(t)
		cli := azdcli.NewCLI(t)
		cli.WorkingDirectory = dir
		cli.Env = append(os.Environ(), input.PromptTimeoutEnvVar+"=-1")

		result, err := cli.RunCommandWithStdIn(testCtx, "", "init")

		require.Error(t, err)
		require.NotNil(t, result)
		output := result.Stdout + result.Stderr
		require.False(t, strings.Contains(output, "prompt timed out"),
			"timeout disabled, should not see timeout message")
	})

	t.Run("invalid value handled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		testCtx, testCancel := context.WithTimeout(ctx, 2*time.Second)
		defer testCancel()

		dir := tempDirWithDiagnostics(t)
		cli := azdcli.NewCLI(t)
		cli.WorkingDirectory = dir
		cli.Env = append(os.Environ(), input.PromptTimeoutEnvVar+"=invalid")

		result, _ := cli.RunCommandWithStdIn(testCtx, "", "init")

		require.NotNil(t, result, "command should complete without crashing")
	})

	t.Run("no-prompt flag works", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		testCtx, testCancel := context.WithTimeout(ctx, 2*time.Second)
		defer testCancel()

		dir := tempDirWithDiagnostics(t)
		cli := azdcli.NewCLI(t)
		cli.WorkingDirectory = dir

		result, err := cli.RunCommand(testCtx, "init", "--no-prompt")

		require.NotNil(t, result)
		if err != nil {
			output := result.Stdout + result.Stderr
			require.False(t, strings.Contains(output, "prompt timed out"),
				"--no-prompt should skip prompts, not timeout")
		}
	})
}
