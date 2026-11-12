// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

func Test_CLI_Extension_ExitCode(t *testing.T) {
	configDir := tempDirWithDiagnostics(t)
	binaryName := "extension-exit"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	extensionPath := filepath.Join("extensions", "test.exit", binaryName)
	buildCtx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	//nolint:gosec // G204: build the repository-owned extension fixture.
	build := exec.CommandContext(buildCtx, "go", "build", "-o", filepath.Join(configDir, extensionPath),
		"./testdata/extension-exit")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "building extension fixture: %s", output)

	cfg := map[string]any{
		"extension": map[string]any{
			"installed": map[string]*extensions.Extension{
				"test.exit": {
					Id: "test.exit", Namespace: "exit-test", Version: "1.0.0", Path: extensionPath,
					Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
				},
			},
		},
	}
	content, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.json"), content, 0o600))

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = tempDirWithDiagnostics(t)
	cli.Env = append(os.Environ(), cli.Env...)
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+configDir, "AZURE_DEV_COLLECT_TELEMETRY=no",
		"AZD_FORCE_TTY=false", "NO_COLOR=1")

	for _, tt := range []struct {
		name       string
		args       []string
		exitCode   int
		stdout     string
		diagnostic string
		suggestion string
	}{
		{name: "Success", args: []string{"0"}, stdout: "{\"exitCode\":0}\n"},
		{name: "Failure", args: []string{"1"}, exitCode: 1, stdout: "{\"exitCode\":1}\n",
			diagnostic: "extension diagnostic"},
		{name: "QualityGate", args: []string{"2"}, exitCode: 2, stdout: "{\"exitCode\":2}\n",
			diagnostic: "extension diagnostic"},
		{name: "OtherExitCode", args: []string{"42"}, exitCode: 42, stdout: "{\"exitCode\":42}\n",
			diagnostic: "extension diagnostic"},
		{name: "StructuredError", args: []string{"2", "structured"}, exitCode: 2, stdout: "{\"exitCode\":2}\n",
			diagnostic: "extension diagnostic", suggestion: "Inspect the evaluation results"},
		{name: "InvalidArgument", args: []string{"invalid"}, exitCode: 1, diagnostic: "invalid exit code"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			args := append([]string{"exit-test"}, tt.args...)
			args = append(args, "--output", "json")
			result, err := cli.RunCommand(ctx, args...)
			require.NotNil(t, result)
			if tt.exitCode == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, tt.exitCode, result.ExitCode, "stdout: %s\nstderr: %s", result.Stdout, result.Stderr)
			if tt.suggestion == "" {
				require.Equal(t, tt.stdout, result.Stdout)
			} else {
				require.True(t, strings.HasPrefix(result.Stdout, tt.stdout))
				require.Contains(t, result.Stdout+result.Stderr, "quality gate not met")
				require.Contains(t, result.Stdout+result.Stderr, tt.suggestion)
			}
			if tt.diagnostic != "" {
				require.Contains(t, result.Stderr, tt.diagnostic)
			}
		})
	}

	t.Run("MissingProgram", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(configDir, extensionPath)))
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		result, err := cli.RunCommand(ctx, "exit-test", "2", "--output", "json")
		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 1, result.ExitCode)
		require.Contains(t, result.Stdout+result.Stderr, "not found")
	})

	t.Run("LaunchFailure", func(t *testing.T) {
		//nolint:gosec // G306: execute permission is needed to reach the invalid-program failure.
		require.NoError(t, os.WriteFile(filepath.Join(configDir, extensionPath), []byte("invalid executable"), 0o700))
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		result, err := cli.RunCommand(ctx, "exit-test", "2", "--output", "json")
		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 1, result.ExitCode)
	})
}
