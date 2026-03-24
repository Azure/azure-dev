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
	if session != nil && session.Playback {
		location = "eastus2"
	}
	require.NotEmpty(t, location, "hooks smoke test requires a location")

	predeployTracePath := filepath.Join(dir, "predeploy-trace.log")
	provisionTracePath := filepath.Join(dir, "provision-trace.log")

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	baseEnv := append(os.Environ(),
		fmt.Sprintf("AZURE_SUBSCRIPTION_ID=%s", subscriptionId),
		fmt.Sprintf("AZURE_LOCATION=%s", location),
		"AZD_ALPHA_ENABLE_LLM=false",
	)
	setHookTraceEnv := func(tracePath string) {
		cli.Env = append([]string{}, baseEnv...)
		cli.Env = append(cli.Env, fmt.Sprintf("HOOK_TRACE_FILE=%s", tracePath))
	}

	readTraceEntries := func(tracePath string) []string {
		traceBytes, err := os.ReadFile(tracePath)
		require.NoError(t, err)

		var traceEntries []string
		for _, line := range strings.Split(string(traceBytes), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			traceEntries = append(traceEntries, line)
		}

		return traceEntries
	}

	err := copySample(dir, "hooks")
	require.NoError(t, err, "failed expanding sample")

	cli.Env = append([]string{}, baseEnv...)
	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	setHookTraceEnv(predeployTracePath)
	_, err = cli.RunCommand(ctx, "hooks", "run", "predeploy", "--service", "app")
	require.NoError(t, err)

	setHookTraceEnv(provisionTracePath)
	_, err = cli.RunCommand(ctx, "provision")
	require.Error(t, err, "provision should fail for this hooks sample")

	require.Equal(t, []string{
		"command-predeploy",
		"service-predeploy",
	}, readTraceEntries(predeployTracePath))

	require.Equal(t, []string{
		"command-preprovision",
		"layer-preprovision",
	}, readTraceEntries(provisionTracePath))
}
