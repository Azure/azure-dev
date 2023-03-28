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

func Test_CLI_EnvCommandsWorkWhenLoggedOut(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	// Create some environments, we do this while we are logged in because creating an
	// environment right now requires you to be logged in since it fetches information
	// about the current account to prompt for a subscription and location.
	envNew(ctx, t, cli, "env1", true)
	envNew(ctx, t, cli, "env2", true)

	// set a private config dir, this well ensure we are logged out.
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)

	// check to make sure we are logged out as expected.
	res, err := cli.RunCommand(ctx, "login", "--check-status", "--output", "json")
	require.NoError(t, err)

	var lr contracts.LoginResult
	err = json.Unmarshal([]byte(res.Stdout), &lr)
	require.NoError(t, err)

	require.Equal(t, contracts.LoginStatusUnauthenticated, lr.Status)

	res, err = cli.RunCommand(ctx, "env", "list", "--output", "json")
	require.NoError(t, err)

	var envs []contracts.EnvListEnvironment
	err = json.Unmarshal([]byte(res.Stdout), &envs)
	require.NoError(t, err)

	// We should see the two environments.
	require.Equal(t, 2, len(envs))
}

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
func Test_CLI_Env_Values_SingleEnvironment(t *testing.T) {
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

// Verifies azd env commands that manage values across different environments.
func Test_CLI_Env_Values_MultipleEnvironments(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	// Create one environment
	envName1 := randomEnvName()
	envNew(ctx, t, cli, envName1, false)

	// Create another environment
	envName2 := randomEnvName()
	envNew(ctx, t, cli, envName2, false)

	// Get and set values via -e flag for first environment
	envSetValue(ctx, t, cli, "envName1", envName1, "--environment", envName1)
	values := envGetValues(ctx, t, cli, "--environment", envName1)
	require.Contains(t, values, "AZURE_ENV_NAME")
	require.Equal(t, values["AZURE_ENV_NAME"], envName1)
	require.Contains(t, values, "envName1")
	require.Equal(t, values["envName1"], envName1)

	// Get and set values via -e flag for the second environment
	envSetValue(ctx, t, cli, "envName2", envName2, "--environment", envName2)
	values = envGetValues(ctx, t, cli, "--environment", envName2)
	require.Contains(t, values, "AZURE_ENV_NAME")
	require.Equal(t, values["AZURE_ENV_NAME"], envName2)
	require.Contains(t, values, "envName2")
	require.Equal(t, values["envName2"], envName2)
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

func envNew(ctx context.Context, t *testing.T, cli *azdcli.CLI, envName string, usePrompt bool, args ...string) {
	defaultArgs := []string{"env", "new"}

	if usePrompt {
		runArgs := append(defaultArgs, args...)
		_, err := cli.RunCommandWithStdIn(ctx, stdinForTests(envName), runArgs...)
		require.NoError(t, err)
	} else {
		runArgs := append(defaultArgs, envName, "--subscription", testSubscriptionId, "-l", defaultLocation)
		runArgs = append(runArgs, args...)
		_, err := cli.RunCommand(ctx, runArgs...)
		require.NoError(t, err)
	}
}

func envList(ctx context.Context, t *testing.T, cli *azdcli.CLI) []contracts.EnvListEnvironment {
	result, err := cli.RunCommand(ctx, "env", "list", "--output", "json")
	require.NoError(t, err)

	env := []contracts.EnvListEnvironment{}
	err = json.Unmarshal([]byte(result.Stdout), &env)
	require.NoError(t, err)

	return env
}

func envSelect(ctx context.Context, t *testing.T, cli *azdcli.CLI, envName string) {
	_, err := cli.RunCommand(ctx, "env", "select", envName)
	require.NoError(t, err)
}

func envSetValue(ctx context.Context, t *testing.T, cli *azdcli.CLI, key string, value string, args ...string) {
	defaultArgs := []string{"env", "set", key, value}
	args = append(defaultArgs, args...)

	_, err := cli.RunCommand(ctx, args...)
	require.NoError(t, err)
}

func envGetValues(ctx context.Context, t *testing.T, cli *azdcli.CLI, args ...string) map[string]string {
	defaultArgs := []string{"env", "get-values", "--output", "json"}
	args = append(defaultArgs, args...)

	result, err := cli.RunCommand(ctx, args...)
	require.NoError(t, err)

	var envValues map[string]string
	err = json.Unmarshal([]byte(result.Stdout), &envValues)
	require.NoError(t, err)

	return envValues
}
