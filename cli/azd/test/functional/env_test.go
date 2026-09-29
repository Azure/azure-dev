// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_CLI_Env_RejectsPathTraversal(t *testing.T) {
	credentialServer := azdcli.StartTestCredentialServer(t)
	t.Cleanup(credentialServer.Close)

	for _, source := range []string{"defaultEnvironment", "flag", "AZURE_ENV_NAME", "directory-link"} {
		t.Run(source, func(t *testing.T) {
			commands := [][]string{
				{"env", "get-values"},
				{"env", "set", "AZURE_SUBSCRIPTION_ID", "modified"},
			}
			// Provision uses the environment flag or project default, not AZURE_ENV_NAME.
			if source != "AZURE_ENV_NAME" {
				commands = append(commands, []string{"provision"})
			}
			for _, command := range commands {
				t.Run(strings.Join(command, "_"), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					dir := t.TempDir()
					trustedRoot := filepath.Join(dir, "trusted-project", ".azure", "prod")
					lowProject := filepath.Join(dir, "low-project")
					require.NoError(t, os.MkdirAll(trustedRoot, 0700))
					require.NoError(t, os.MkdirAll(filepath.Join(lowProject, ".azure"), 0700))
					require.NoError(t, os.WriteFile(filepath.Join(lowProject, "azure.yaml"),
						[]byte("name: low-project\nservices: {}\n"), 0600))

					envPath := filepath.Join(trustedRoot, ".env")
					configPath := filepath.Join(trustedRoot, "config.json")
					envBytes := []byte("AZURE_ENV_NAME=prod\nTRUSTED_MARKER=not-for-low-project\n" +
						"AZURE_SUBSCRIPTION_ID=00000000-0000-0000-0000-000000000001\nAZURE_LOCATION=eastus\n")
					configBytes := []byte(`{"trusted":"unchanged"}`)
					require.NoError(t, os.WriteFile(envPath, envBytes, 0600))
					require.NoError(t, os.WriteFile(configPath, configBytes, 0600))

					cli := azdcli.NewCLI(t)
					cli.WorkingDirectory = lowProject
					cli.Env = append(os.Environ(), "AZD_CONFIG_DIR="+t.TempDir(),
						"AZURE_ENV_NAME=", "AZD_FORCE_TTY=false", "NO_COLOR=1", "AZURE_DEV_COLLECT_TELEMETRY=no",
						"CI=1", "AZD_AUTH_ENDPOINT="+credentialServer.URL, "AZD_AUTH_KEY=local-test-key")
					const invalidName = "../../trusted-project/.azure/prod"
					expectedError := "is invalid"
					args := append([]string{"--no-prompt"}, command...)
					switch source {
					case "defaultEnvironment":
						require.NoError(t, os.WriteFile(filepath.Join(lowProject, ".azure", "config.json"),
							[]byte(`{"defaultEnvironment":"`+invalidName+`"}`), 0600))
					case "flag":
						args = append(args, "--environment", invalidName)
					case "AZURE_ENV_NAME":
						cli.Env = append(cli.Env, "AZURE_ENV_NAME="+invalidName)
					case "directory-link":
						ostest.DirectoryLink(t, trustedRoot, filepath.Join(lowProject, ".azure", "prod"))
						require.NoError(t, os.WriteFile(filepath.Join(lowProject, ".azure", "config.json"),
							[]byte(`{"defaultEnvironment":"prod"}`), 0600))
						expectedError = "must not be a symbolic link or reparse point"
					}

					result, err := cli.RunCommand(ctx, args...)
					require.Error(t, err)
					require.NotNil(t, result)
					require.Contains(t, result.Stdout+result.Stderr, expectedError)
					require.NotContains(t, result.Stdout+result.Stderr, "not-for-low-project")
					actualEnv, err := os.ReadFile(envPath)
					require.NoError(t, err)
					require.Equal(t, envBytes, actualEnv)
					actualConfig, err := os.ReadFile(configPath)
					require.NoError(t, err)
					require.Equal(t, configBytes, actualConfig)
					require.NoFileExists(t, filepath.Join(trustedRoot, ".env.lock"))
				})
			}
		})
	}
}

func Test_CLI_Env_ListSkipsInvalidEntries(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".azure")
	require.NoError(t, os.MkdirAll(filepath.Join(base, "dev"), 0700))
	require.NoError(t, os.Mkdir(filepath.Join(base, "my env"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"),
		[]byte("name: test-project\nservices: {}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(base, "config.json"),
		[]byte(`{"defaultEnvironment":"dev"}`), 0600))
	ostest.DirectoryLink(t, t.TempDir(), filepath.Join(base, "linked"))

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZD_CONFIG_DIR="+t.TempDir(),
		"AZURE_ENV_NAME=", "AZD_FORCE_TTY=false", "NO_COLOR=1", "AZURE_DEV_COLLECT_TELEMETRY=no", "CI=1")
	result, err := cli.RunCommand(t.Context(), "env", "list", "--output", "json")
	require.NoError(t, err)
	var envs []contracts.EnvListEnvironment
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &envs))
	require.Len(t, envs, 1)
	require.Equal(t, "dev", envs[0].Name)
	require.True(t, envs[0].IsDefault)
}

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

	// set a private config dir, this will ensure we are logged out.
	configDir := t.TempDir()
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+configDir)
	// disable telemetry that would write to the temporary directory we create here
	cli.Env = append(cli.Env, "AZURE_DEV_COLLECT_TELEMETRY=no")

	// check to make sure we are logged out as expected.
	res, err := cli.RunCommand(ctx, "auth", "login", "--check-status", "--output", "json")
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

	// Verify that trying to select an environment which does not exist fails.
	res, err := cli.RunCommand(ctx, "env", "select", "does-not-exist")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "environment 'does-not-exist' does not exist")

	// Verify that running refresh with an explicit env name from an argument and from a flag leads to an error.
	_, err = cli.RunCommand(t.Context(), "env", "refresh", "-e", "from-flag", "from-arg")
	require.Error(t, err)

	// Verify creating an environment when no default environment is set
	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)
	err = azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: ""})
	require.NoError(t, err)

	// Here we choose 'monitor' as the command that requires an environment to target
	cmdNeedingEnv := []string{"monitor"}

	envName3 := randomEnvName()
	_, _ = cli.RunCommandWithStdIn(
		ctx,
		"Create a new environment\n"+envName3+"\ny\n",
		cmdNeedingEnv...)
	environmentList = envList(ctx, t, cli)
	require.Len(t, environmentList, 3)
	requireIsDefault(t, environmentList, envName3)

	// Verify selecting an environment when no default environment is set
	err = azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: ""})
	require.NoError(t, err)

	_, _ = cli.RunCommandWithStdIn(
		ctx,
		envName2+"\n",
		cmdNeedingEnv...)

	environmentList = envList(ctx, t, cli)
	require.Len(t, environmentList, 3)
	requireIsDefault(t, environmentList, envName2)
}

func Test_CLI_Env_Values_Json(t *testing.T) {
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
	values := envGetValues(ctx, t, cli, "--output", "json")
	require.Contains(t, values, "key1")
	require.Equal(t, values["key1"], "value1")

	// Add key2
	envSetValue(ctx, t, cli, "key2", "value2")
	values = envGetValues(ctx, t, cli, "--output", "json")
	require.Contains(t, values, "key2")
	require.Equal(t, values["key2"], "value2")

	// Modify key1
	envSetValue(ctx, t, cli, "key1", "modified1")
	values = envGetValues(ctx, t, cli, "--output", "json")
	require.Contains(t, values, "key1")
	require.Equal(t, values["key1"], "modified1")
	require.Contains(t, values, "key2")
	require.Equal(t, values["key2"], "value2")
}

func Test_CLI_Env_Values(t *testing.T) {
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

	values := envGetValues(ctx, t, cli)
	require.Contains(t, values, "AZURE_ENV_NAME")
	require.Equal(t, envName2, values["AZURE_ENV_NAME"])

	// Get and set values via -e flag for first environment
	envSetValue(ctx, t, cli, "envName1", envName1, "--environment", envName1)
	values = envGetValues(ctx, t, cli, "--environment", envName1)
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

func Test_CLI_Env_Remove(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	// Verify removing when no environment is present
	res, err := cli.RunCommand(ctx, "env", "remove")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "required arguments not provided")

	// Create two environments
	envName1 := randomEnvName()
	envNew(ctx, t, cli, envName1, false)
	envName2 := randomEnvName()
	envNew(ctx, t, cli, envName2, false)

	require.Len(t, envList(ctx, t, cli), 2)

	// Remove via positional arg with --force
	envRemove(ctx, t, cli, envName1)
	require.Len(t, envList(ctx, t, cli), 1)

	// Remove via -e flag with --force (also tests 'rm' alias)
	_, err = cli.RunCommand(ctx, "env", "rm", "-e", envName2, "--force")
	require.NoError(t, err)
	require.Len(t, envList(ctx, t, cli), 0)

	// Verify removing non-existent environment fails
	res, err = cli.RunCommand(ctx, "env", "remove", "does-not-exist", "--force")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "does not exist")

	// Verify conflicting -e and positional arg fails
	envNew(ctx, t, cli, "test-env", false)
	_, err = cli.RunCommand(ctx, "env", "remove", "from-arg", "-e", "from-flag", "--force")
	require.Error(t, err)
}

func Test_CLI_Env_GetValue(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	// Create an environment
	envName := randomEnvName()
	envNew(ctx, t, cli, envName, false)

	// Set key1
	envSetValue(ctx, t, cli, "key1", "value1")

	// Get key1 value
	value := envGetValue(ctx, t, cli, "key1")
	require.Equal(t, "value1", value)

	// Set key2
	envSetValue(ctx, t, cli, "key2", "value2")

	// Get key2 value
	value = envGetValue(ctx, t, cli, "key2")
	require.Equal(t, "value2", value)

	// Modify key1
	envSetValue(ctx, t, cli, "key1", "modified1")

	// Get modified key1 value
	value = envGetValue(ctx, t, cli, "key1")
	require.Equal(t, "modified1", value)

	// Test non-existent key
	res, err := cli.RunCommand(ctx, "env", "get-value", "non_existent_key")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "key not found in environment values: 'non_existent_key'")
}

func envGetValue(ctx context.Context, t *testing.T, cli *azdcli.CLI, key string) string {
	args := []string{"env", "get-value", key}

	result, err := cli.RunCommand(ctx, args...)
	require.NoError(t, err)

	return strings.TrimSpace(result.Stdout)
}

func requireIsDefault(t *testing.T, list []contracts.EnvListEnvironment, envName string) {
	for _, env := range list {
		if env.Name == envName {
			require.True(t, env.IsDefault)
			return
		}
	}

	require.Failf(t, "environment not found",
		"%#v does not contain env with name %#v", list, envName)
}

func envNew(ctx context.Context, t *testing.T, cli *azdcli.CLI, envName string, usePrompt bool, args ...string) {
	defaultArgs := []string{"env", "new"}

	if usePrompt {
		runArgs := append(defaultArgs, args...)
		_, err := cli.RunCommandWithStdIn(ctx, envName+"\ny\n", runArgs...)
		require.NoError(t, err)
	} else {
		runArgs := append(defaultArgs, envName, "--no-prompt", "--subscription", cfg.SubscriptionID, "-l", cfg.Location)
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

// Test_CLI_Env_List_Query tests the --query flag for JMESPath filtering on env list.
func Test_CLI_Env_List_Query(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	// Create two environments
	envName1 := randomEnvName()
	envNew(ctx, t, cli, envName1, false)

	envName2 := randomEnvName()
	envNew(ctx, t, cli, envName2, false)

	// Test --query with --output json to extract just the names
	result, err := cli.RunCommand(ctx, "env", "list", "--output", "json", "--query", "[].Name")
	require.NoError(t, err)

	var names []string
	err = json.Unmarshal([]byte(result.Stdout), &names)
	require.NoError(t, err)
	require.Len(t, names, 2)
	require.Contains(t, names, envName1)
	require.Contains(t, names, envName2)

	// Test --query with -o json (short form)
	result, err = cli.RunCommand(ctx, "env", "list", "-o", "json", "--query", "[].Name")
	require.NoError(t, err)

	err = json.Unmarshal([]byte(result.Stdout), &names)
	require.NoError(t, err)
	require.Len(t, names, 2)

	// Test --query filtering for default environment
	result, err = cli.RunCommand(ctx, "env", "list", "--output", "json", "--query", "[?IsDefault].Name")
	require.NoError(t, err)

	var defaultNames []string
	err = json.Unmarshal([]byte(result.Stdout), &defaultNames)
	require.NoError(t, err)
	require.Len(t, defaultNames, 1)
	// envName2 should be default since it was created last
	require.Equal(t, envName2, defaultNames[0])

	// Test --query requires --output json
	_, err = cli.RunCommand(ctx, "env", "list", "--output", "table", "--query", "[].Name")
	require.Error(t, err)
}

func envSelect(ctx context.Context, t *testing.T, cli *azdcli.CLI, envName string) {
	_, err := cli.RunCommand(ctx, "env", "select", envName)
	require.NoError(t, err)
}

func envRemove(ctx context.Context, t *testing.T, cli *azdcli.CLI, envName string) {
	_, err := cli.RunCommand(ctx, "env", "remove", envName, "--force")
	require.NoError(t, err)
}

func envSetValue(ctx context.Context, t *testing.T, cli *azdcli.CLI, key string, value string, args ...string) {
	defaultArgs := []string{"env", "set", key, value}
	args = append(defaultArgs, args...)

	_, err := cli.RunCommand(ctx, args...)
	require.NoError(t, err)
}

func envGetValues(
	ctx context.Context,
	t *testing.T,
	cli *azdcli.CLI,
	args ...string) map[string]string {
	defaultArgs := []string{"env", "get-values"}
	args = append(defaultArgs, args...)

	result, err := cli.RunCommand(ctx, args...)
	require.NoError(t, err)

	outputMode := azdcli.GetOutputFlagValue(args)

	envValues := map[string]string{}

	switch outputMode {
	case "json":
		err = json.Unmarshal([]byte(result.Stdout), &envValues)
		require.NoError(t, err)
	case "", "none":
		scanner := bufio.NewScanner(strings.NewReader(result.Stdout))
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.Contains(line, "=") {
				t.Fatal("unexpected line in output: " + line)
			}

			parts := strings.Split(line, "=")
			require.Len(t, parts, 2)
			val, err := unquote(parts[1])
			require.NoError(t, err)
			envValues[parts[0]] = val
		}
	default:
		panic("unhandled output mode: " + outputMode)
	}

	return envValues
}

func unquote(s string) (string, error) {
	if len(s) == 0 {
		return s, nil
	}

	if s[0] == '"' || s[0] == '\'' {
		if len(s) == 1 || s[len(s)-1] != s[0] {
			return "", fmt.Errorf("unmatched quote in: %s", s)
		}

		return s[1 : len(s)-1], nil
	}

	return s, nil
}
