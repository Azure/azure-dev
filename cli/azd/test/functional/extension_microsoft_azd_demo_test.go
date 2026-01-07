// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

// Test_CLI_Extension_Context tests the demo extension's context command.
// This test verifies that:
// 1. The demo extension can be installed
// 2. The "azd demo context" command successfully retrieves project and environment context after init
// 3. After provisioning, the command displays deployment context with Azure resources
// 4. The extension's gRPC callbacks to azd work correctly
// Note: The gRPC callbacks are inter-process communication only. The actual Azure HTTPS calls made by azd
// are recorded and can be played back, making this test suitable for both live and playback modes.
func Test_CLI_Extension_Context(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	session := recording.Start(t)

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	// Generate environment name early (before session starts)
	// so we can use it for both extension installation and the recorded part
	envName := randomOrStoredEnvName(session)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	// Step 1: Install the demo extension (not recorded - blob is too large)
	cliNoSession := azdcli.NewCLI(t)
	cliNoSession.WorkingDirectory = dir
	cliNoSession.Env = append(cliNoSession.Env, os.Environ()...)
	cliNoSession.Env = append(cliNoSession.Env, "AZURE_LOCATION=eastus2")

	t.Log("Installing demo extension")
	_, err := cliNoSession.RunCommand(ctx, "ext", "install", "microsoft.azd.demo", "-s", "azd")
	require.NoError(t, err)

	// Cleanup: Uninstall extension at the end of the test
	defer func() {
		t.Log("Uninstalling demo extension")
		_, _ = cliNoSession.RunCommand(ctx, "ext", "uninstall", "microsoft.azd.demo")
	}()

	// Step 2: Create CLI with session for recorded operations
	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	defer cleanupDeployments(ctx, t, cli, session, envName)

	// Step 3: Copy the storage sample
	err = copySample(dir, "storage")
	require.NoError(t, err, "failed copying sample")

	// Step 4: Initialize the project
	t.Log("Initializing project")
	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// Step 5: Run azd demo context command BEFORE provisioning
	// At this point, we should have project and environment info, but no deployment context yet
	t.Log("Running azd demo context command (before provision)")
	result, err := cli.RunCommand(ctx, "demo", "context")
	require.NoError(t, err)

	outputBeforeProvision := result.Stdout

	// Verify project context is displayed
	require.Contains(t, outputBeforeProvision, "Project:", "Output should contain project information")

	// Verify environment context is displayed
	require.Contains(t, outputBeforeProvision, "Environments:", "Output should contain environments list")
	require.Contains(t, outputBeforeProvision, envName, "Output should contain the environment name")
	require.Contains(t, outputBeforeProvision, "(selected)", "Output should show the selected environment")

	// Verify environment values are displayed (AZURE_LOCATION should be set during init)
	require.Contains(t, outputBeforeProvision, "Environment values:", "Output should contain environment values")

	t.Log("Successfully verified azd demo context output before provisioning")

	// Step 6: Provision infrastructure to create deployment context
	t.Log("Provisioning infrastructure")
	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)

	// Step 7: Run azd demo context command AFTER provisioning
	// This command makes gRPC callbacks to retrieve:
	// - User configuration
	// - Project information
	// - Environment list and current environment
	// - Environment values
	// - Deployment context (subscription, resource group, resources)
	t.Log("Running azd demo context command (after provision)")
	result, err = cli.RunCommand(ctx, "demo", "context")
	require.NoError(t, err)

	// Step 8: Verify the output contains expected context information including deployment details
	outputAfterProvision := result.Stdout

	// Verify project context is still displayed
	require.Contains(t, outputAfterProvision, "Project:", "Output should contain project information")

	// Verify environment context is still displayed
	require.Contains(t, outputAfterProvision, "Environments:", "Output should contain environments list")
	require.Contains(t, outputAfterProvision, envName, "Output should contain the environment name")

	// Verify deployment context is now displayed
	require.Contains(t, outputAfterProvision, "Deployment Context:", "Output should contain deployment context")
	require.Contains(t, outputAfterProvision, "Subscription ID:", "Output should contain subscription ID")
	require.Contains(t, outputAfterProvision, "Resource Group:", "Output should contain resource group")

	// Verify provisioned resources are displayed (storage sample should provision storage account)
	require.Contains(t, outputAfterProvision, "Provisioned Azure Resources:",
		"Output should contain provisioned resources section")

	t.Log("Successfully verified azd demo context command output after provisioning")

	// Step 9: Clean up resources with azd down
	t.Log("Running azd down to clean up resources")
	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)

	t.Log("Successfully cleaned up resources with azd down")
}
