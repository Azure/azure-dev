// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

// Test_CLI_ServiceEnv_Build validates that service-level environment variables
// defined in azure.yaml are properly expanded and passed to build tools.
func Test_CLI_ServiceEnv_Build(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "swaenvtest")
	require.NoError(t, err, "failed expanding sample")

	// Initialize the project
	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// Set environment variables that will be referenced in azure.yaml
	_, err = cli.RunCommand(ctx, "env", "set", "TEST_API_URL", "https://api.example.com")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "set", "TEST_APP_NAME", "MyTestApp")
	require.NoError(t, err)

	// Run the build command which should pass env vars to npm build
	result, err := cli.RunCommand(ctx, "build", "web")
	require.NoError(t, err, "build failed: %s", result.Stdout)

	// Verify the env-output.json file was created with correct values
	envOutputPath := filepath.Join(dir, "dist", "env-output.json")
	envOutputBytes, err := os.ReadFile(envOutputPath)
	require.NoError(t, err, "env-output.json should be created by build")

	var envOutput map[string]string
	err = json.Unmarshal(envOutputBytes, &envOutput)
	require.NoError(t, err, "env-output.json should be valid JSON")

	// Verify the environment variables were expanded correctly from azd env
	require.Equal(t, "https://api.example.com", envOutput["VITE_API_URL"],
		"VITE_API_URL should be expanded from ${TEST_API_URL}")
	require.Equal(t, "MyTestApp", envOutput["VITE_APP_NAME"],
		"VITE_APP_NAME should be expanded from ${TEST_APP_NAME}")
	require.Equal(t, "test", envOutput["VITE_BUILD_ENV"],
		"VITE_BUILD_ENV should be the static value 'test'")
}

// Test_CLI_ServiceEnv_Package validates that service-level environment variables
// are passed to build tools during the package command.
func Test_CLI_ServiceEnv_Package(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "swaenvtest")
	require.NoError(t, err, "failed expanding sample")

	// Initialize the project
	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// Set environment variables that will be referenced in azure.yaml
	_, err = cli.RunCommand(ctx, "env", "set", "TEST_API_URL", "https://package.example.com")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "set", "TEST_APP_NAME", "PackageTestApp")
	require.NoError(t, err)

	// Run the package command which includes building
	result, err := cli.RunCommand(ctx, "package", "web")
	require.NoError(t, err, "package failed: %s", result.Stdout)

	// Verify the env-output.json file was created with correct values
	envOutputPath := filepath.Join(dir, "dist", "env-output.json")
	envOutputBytes, err := os.ReadFile(envOutputPath)
	require.NoError(t, err, "env-output.json should be created during package build step")

	var envOutput map[string]string
	err = json.Unmarshal(envOutputBytes, &envOutput)
	require.NoError(t, err, "env-output.json should be valid JSON")

	// Verify the environment variables were expanded correctly
	require.Equal(t, "https://package.example.com", envOutput["VITE_API_URL"])
	require.Equal(t, "PackageTestApp", envOutput["VITE_APP_NAME"])
	require.Equal(t, "test", envOutput["VITE_BUILD_ENV"])
}
