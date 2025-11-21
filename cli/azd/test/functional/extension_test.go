// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

// Test_CLI_Extension_Capabilities tests the extension framework capabilities using the demo extension.
// This test verifies that:
// 1. The demo extension can be built and installed
// 2. The extension-capabilities sample project can run through the full azd up workflow
// 3. The extension's framework service manager handles restore, build, package operations correctly
func Test_CLI_Extension_Capabilities(t *testing.T) {
	// Skip in playback mode: extensions make Azure calls through gRPC callbacks that are difficult to record
	// The extension workflow calls back into azd commands which would need complex recording setup
	session := recording.Start(t)
	if session != nil && session.Playback {
		t.Skip("Skipping test in playback mode. This test is live only.")
	}

	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	// Generate environment name early (before session starts)
	// so we can use it for both extension installation and the recorded part
	envName := randomOrStoredEnvName(session)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	// Step 1: Build and install extensions (not recorded)
	// Create a CLI without session for extension operations
	cliNoSession := azdcli.NewCLI(t)
	cliNoSession.WorkingDirectory = dir
	cliNoSession.Env = append(cliNoSession.Env, os.Environ()...)
	cliNoSession.Env = append(cliNoSession.Env, "AZURE_LOCATION=eastus2")

	t.Log("Installing microsoft.azd.extensions extension")
	_, err := cliNoSession.RunCommand(ctx, "ext", "install", "microsoft.azd.extensions", "-s", "azd")
	require.NoError(t, err)

	// Build the demo extension
	t.Log("Building demo extension")
	sourcePath := azdcli.GetSourcePath()
	demoExtPath := filepath.Join(sourcePath, "extensions", "microsoft.azd.demo")
	cliForExtBuild := azdcli.NewCLI(t)
	cliForExtBuild.WorkingDirectory = demoExtPath
	cliForExtBuild.Env = append(cliForExtBuild.Env, os.Environ()...)

	// Add azd binary directory to PATH so 'azd x publish' can find azd
	azdDir := filepath.Dir(cliNoSession.AzdPath)
	pathEnv := "PATH=" + azdDir + string(os.PathListSeparator) + os.Getenv("PATH")
	cliForExtBuild.Env = append(cliForExtBuild.Env, pathEnv)

	_, err = cliForExtBuild.RunCommand(ctx, "x", "build")
	require.NoError(t, err)

	_, err = cliForExtBuild.RunCommand(ctx, "x", "pack")
	require.NoError(t, err)

	_, err = cliForExtBuild.RunCommand(ctx, "x", "publish")
	require.NoError(t, err)

	// Install the demo extension from local source
	t.Log("Installing demo extension from local source")
	_, err = cliNoSession.RunCommand(ctx, "ext", "install", "microsoft.azd.demo", "-s", "local")
	require.NoError(t, err)

	// Cleanup: Uninstall extensions at the end of the test
	defer func() {
		t.Log("Uninstalling demo extension")
		_, _ = cliNoSession.RunCommand(ctx, "ext", "uninstall", "microsoft.azd.demo")

		t.Log("Uninstalling microsoft.azd.extensions extension")
		_, _ = cliNoSession.RunCommand(ctx, "ext", "uninstall", "microsoft.azd.extensions")
	}()

	// Step 2: Create CLI with session for recorded operations
	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	defer cleanupDeployments(ctx, t, cli, session, envName)

	// Copy the extension-capabilities sample
	err = copySample(dir, "extension-capabilities")
	require.NoError(t, err, "failed copying sample")

	// Step 3: Initialize the project
	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// Step 4: Run azd up
	// The up command will:
	// - Provision infrastructure (main.bicep)
	// - Restore dependencies via the demo extension
	// - Build the project via the demo extension
	// - Package the project via the demo extension (buildpacks)
	// - Deploy to Azure
	t.Log("Running azd up")
	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "up")
	require.NoError(t, err)
}
