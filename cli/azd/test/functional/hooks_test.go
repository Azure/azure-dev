// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

func Test_CLI_Hooks_RegistrationAndRun(t *testing.T) {
	t.Parallel()

	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	deployTracePath := filepath.Join(dir, "deploy-trace.log")
	provisionTracePath := filepath.Join(dir, "provision-trace.log")

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, fmt.Sprintf("AZURE_LOCATION=%s", cfg.Location))
	cli.Env = append(cli.Env, fmt.Sprintf("AZURE_SUBSCRIPTION_ID=%s", cfg.SubscriptionID))

	err := copySample(dir, "hooks")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	cli.Env = append(cli.Env, fmt.Sprintf("HOOK_TRACE_FILE=%s", deployTracePath))
	require.NoError(t, os.WriteFile(deployTracePath, []byte{}, 0600))
	_, err = cli.RunCommand(ctx, "deploy")
	require.Error(t, err, "deploy should fail for this hooks sample")

	cli.Env = append(cli.Env, fmt.Sprintf("HOOK_TRACE_FILE=%s", provisionTracePath))
	require.NoError(t, os.WriteFile(provisionTracePath, []byte{}, 0600))
	_, err = cli.RunCommand(ctx, "provision")
	require.Error(t, err, "provision should fail for this hooks sample")

	require.Equal(t, []string{
		"command-predeploy",
		"service-prerestore",
	}, readTraceEntries(t, deployTracePath))

	require.Equal(t, []string{
		"command-preprovision",
		"layer-preprovision",
	}, readTraceEntries(t, provisionTracePath))
}

func Test_CLI_Hooks_Run_RegistrationAndRun(t *testing.T) {
	t.Parallel()

	t.Run("RunAll", func(t *testing.T) {
		traceEntries, err := runLocalHooksCommand(t, "predeploy")
		require.NoError(t, err)

		require.Equal(t, []string{
			"command-predeploy",
			"service-predeploy",
		}, traceEntries)
	})

	t.Run("RunSpecific", func(t *testing.T) {
		t.Run("Service", func(t *testing.T) {
			traceEntries, err := runLocalHooksCommand(t, "predeploy", "--service", "app")
			require.NoError(t, err)

			require.Equal(t, []string{
				"command-predeploy",
				"service-predeploy",
			}, traceEntries)
		})

		t.Run("Layer", func(t *testing.T) {
			traceEntries, err := runLocalHooksCommand(t, "preprovision", "--layer", "core")
			require.Error(t, err)

			require.Equal(t, []string{
				"command-preprovision",
				"layer-preprovision",
			}, traceEntries)
		})
	})
}

func runLocalHooksCommand(t *testing.T, args ...string) ([]string, error) {
	t.Helper()

	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	tracePath := filepath.Join(dir, "hooks-run-trace.log")
	require.NoError(t, os.WriteFile(tracePath, []byte{}, 0600))

	err := copySample(dir, "hooks")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	cli.Env = append(cli.Env, fmt.Sprintf("HOOK_TRACE_FILE=%s", tracePath))
	command := append([]string{"hooks", "run"}, args...)
	_, err = cli.RunCommand(ctx, command...)

	return readTraceEntries(t, tracePath), err
}

func readTraceEntries(t *testing.T, tracePath string) []string {
	t.Helper()

	traceBytes, err := os.ReadFile(tracePath)
	require.NoError(t, err)

	var traceEntries []string
	for line := range strings.SplitSeq(string(traceBytes), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		traceEntries = append(traceEntries, line)
	}

	return traceEntries
}
