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
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

func Test_CLI_Hooks_RegistrationAndRun(t *testing.T) {
	t.Parallel()

	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	session := recording.Start(t)

	envName := randomOrStoredEnvName(session)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	subscriptionId := cfgOrStoredSubscription(session)
	require.NotEmpty(t, subscriptionId, "hooks smoke test requires a subscription id")

	location := cfg.Location
	require.NotEmpty(t, location, "hooks smoke test requires a location")

	deployTracePath := filepath.Join(dir, "deploy-trace.log")
	provisionTracePath := filepath.Join(dir, "provision-trace.log")

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	baseEnv := hooksTestEnv(subscriptionId, location)
	cli.Env = append(os.Environ(), baseEnv...)

	err := copySample(dir, "hooks")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	setHookTraceEnv(cli, baseEnv, deployTracePath)
	_, err = cli.RunCommand(ctx, "deploy")
	require.Error(t, err, "deploy should fail for this hooks sample")

	setHookTraceEnv(cli, baseEnv, provisionTracePath)
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

func hooksTestEnv(subscriptionId string, location string) []string {
	baseEnv := append(os.Environ(), "AZD_ALPHA_ENABLE_LLM=false")
	if subscriptionId != "" {
		baseEnv = append(baseEnv, fmt.Sprintf("AZURE_SUBSCRIPTION_ID=%s", subscriptionId))
	}

	if location != "" {
		baseEnv = append(baseEnv, fmt.Sprintf("AZURE_LOCATION=%s", location))
	}

	return baseEnv
}

func setHookTraceEnv(cli *azdcli.CLI, baseEnv []string, tracePath string) {
	cli.Env = append([]string{}, baseEnv...)
	cli.Env = append(cli.Env, fmt.Sprintf("HOOK_TRACE_FILE=%s", tracePath))
}

func runLocalHooksCommand(t *testing.T, args ...string) ([]string, error) {
	t.Helper()

	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomOrStoredEnvName(nil)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	baseEnv := hooksTestEnv("", "")
	tracePath := filepath.Join(dir, "hooks-run-trace.log")

	err := copySample(dir, "hooks")
	require.NoError(t, err, "failed expanding sample")

	cli.Env = append([]string{}, baseEnv...)
	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	setHookTraceEnv(cli, baseEnv, tracePath)

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
