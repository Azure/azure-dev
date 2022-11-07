// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

// Verifies azd env commands that manage environments.
func Test_CLI_Env_Management(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	// Create one environment via interactive prompt
	envName := randomEnvName()
	envNew(ctx, t, cli, envName, true)

	// Verify env list is updated
	environmentList := envList(ctx, t, cli)
	require.Len(t, environmentList, 1)
	requireIsDefault(t, environmentList, envName)

	// Create one environment via flags
	envName2 := randomEnvName()
	envNew(ctx, t, cli, envName2, false)

	// Verify env list is updated, and with new default set
	environmentList = envList(ctx, t, cli)
	require.Len(t, environmentList, 2)
	requireIsDefault(t, environmentList, envName2)

	// Select old environment
	envSelect(ctx, t, cli, envName)

	// Verify env list has new default set
	environmentList = envList(ctx, t, cli)
	require.Len(t, environmentList, 2)
	requireIsDefault(t, environmentList, envName)
}

// Verifies azd env commands that manage environment values.
func Test_CLI_Env_Values_Management(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	// Create one environment
	envName := randomEnvName()
	envNew(ctx, t, cli, envName, false)
	t.Logf("DIR: %s", dir)

	// Add key1
	envSetValue(ctx, t, cli, "key1", "value1")
	values := envGetValues(ctx, t, cli)
	require.Contains(t, values, "key1")
	require.Equal(t, values["key1"], "value1")

	// Add key2
	envSetValue(ctx, t, cli, "key2", "value2")
	values = envGetValues(ctx, t, cli)
	require.Contains(t, values, "key2")
	require.Equal(t, values["key2"], "value2")

	// Modify key1
	envSetValue(ctx, t, cli, "key1", "modified1")
	values = envGetValues(ctx, t, cli)
	require.Contains(t, values, "key1")
	require.Equal(t, values["key1"], "modified1")
	require.Contains(t, values, "key2")
	require.Equal(t, values["key2"], "value2")
}

func requireIsDefault(t *testing.T, list []contracts.EnvListEnvironment, envName string) {
	for _, env := range list {
		if env.Name == envName {
			require.True(t, env.IsDefault)
			return
		}
	}

	require.Fail(t, "%#v does not contain env with name %#v", list, envName)
}

func envNew(ctx context.Context, t *testing.T, cli *azdcli.CLI, envName string, usePrompt bool) {
	if usePrompt {
		_, err := cli.RunCommandWithStdIn(ctx, stdinForTests(envName), "env", "new")
		require.NoError(t, err)
	} else {
		_, err := cli.RunCommand(ctx, "env", "new", envName, "--subscription", testSubscriptionId, "-l", defaultLocation)
		require.NoError(t, err)
	}
}

func envList(ctx context.Context, t *testing.T, cli *azdcli.CLI) []contracts.EnvListEnvironment {
	jsonOutput, err := cli.RunCommand(ctx, "env", "list", "--output", "json")
	require.NoError(t, err)

	env := []contracts.EnvListEnvironment{}
	err = json.Unmarshal([]byte(jsonOutput), &env)
	require.NoError(t, err)

	return env
}

func envSelect(ctx context.Context, t *testing.T, cli *azdcli.CLI, envName string) {
	_, err := cli.RunCommand(ctx, "env", "select", envName)
	require.NoError(t, err)
}

func envSetValue(ctx context.Context, t *testing.T, cli *azdcli.CLI, key string, value string) {
	_, err := cli.RunCommand(ctx, "env", "set", key, value)
	require.NoError(t, err)
}

func envGetValues(ctx context.Context, t *testing.T, cli *azdcli.CLI) map[string]string {
	jsonOutput, err := cli.RunCommand(ctx, "env", "get-values", "--output", "json")
	require.NoError(t, err)

	var envValues map[string]string
	err = json.Unmarshal([]byte(jsonOutput), &envValues)
	require.NoError(t, err)

	return envValues
}
