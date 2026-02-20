// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

// Test_CLI_Extension_ForceInstall tests the --force flag behavior for extension install.
// This test verifies that:
// 1. Installing an extension works normally
// 2. Downgrading with --force installs the lower version
// 3. The installed version is verified to be the lower version
// 4. Installing without --force skips reinstall when the requested version matches the installed version
// 5. Installing with --force reinstalls even when the requested version matches the installed version
func Test_CLI_Extension_ForceInstall(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	cli := azdcli.NewCLI(t)
	cli.Env = append(cli.Env, os.Environ()...)

	// Setup: Add local extension source
	sourcePath := azdcli.GetSourcePath()
	registryPath := filepath.Join(sourcePath, "extensions", "registry.json")
	t.Logf("Adding local extension source from: %s", registryPath)
	_, err := cli.RunCommand(ctx, "ext", "source", "add", "-n", "test-local", "-t", "file", "-l", registryPath)
	require.NoError(t, err)

	// Cleanup function to ensure extension is uninstalled and source removed
	defer func() {
		t.Log("Cleaning up: uninstalling microsoft.azd.demo extension")
		_, _ = cli.RunCommand(ctx, "ext", "uninstall", "microsoft.azd.demo")
		t.Log("Cleaning up: removing test-local source")
		_, _ = cli.RunCommand(ctx, "ext", "source", "remove", "test-local")
	}()

	// Step 1: Install the latest version of microsoft.azd.demo extension
	t.Log("Installing microsoft.azd.demo extension (latest version)")
	result, err := cli.RunCommand(ctx, "ext", "install", "microsoft.azd.demo", "-s", "test-local")
	require.NoError(t, err)
	require.Contains(t, result.Stdout, "microsoft.azd.demo")

	// Step 2: List installed extensions and get the current version
	t.Log("Checking installed version")
	result, err = cli.RunCommand(ctx, "ext", "list", "--installed", "--output", "json")
	require.NoError(t, err)

	var installedExtensions []struct {
		ID               string `json:"id"`
		Version          string `json:"version"`
		InstalledVersion string `json:"installedVersion"`
	}
	err = json.Unmarshal([]byte(result.Stdout), &installedExtensions)
	require.NoError(t, err)

	var installedVersion string
	for _, ext := range installedExtensions {
		if ext.ID == "microsoft.azd.demo" {
			installedVersion = ext.InstalledVersion
			break
		}
	}
	require.NotEmpty(t, installedVersion, "microsoft.azd.demo should be installed")
	t.Logf("Currently installed version: %s", installedVersion)

	// Step 3: Try to downgrade to version 0.3.0 with --force
	targetVersion := "0.3.0"
	t.Logf("Downgrading to version %s with --force flag", targetVersion)
	result, err = cli.RunCommand(
		ctx, "ext", "install", "microsoft.azd.demo", "-s", "test-local", "-v", targetVersion, "--force")
	require.NoError(t, err)
	require.Contains(t, result.Stdout, "microsoft.azd.demo")

	// Step 4: Verify the downgrade was successful
	t.Log("Verifying downgraded version")
	result, err = cli.RunCommand(ctx, "ext", "list", "--installed", "--output", "json")
	require.NoError(t, err)

	err = json.Unmarshal([]byte(result.Stdout), &installedExtensions)
	require.NoError(t, err)

	var downgradedVersion string
	for _, ext := range installedExtensions {
		if ext.ID == "microsoft.azd.demo" {
			downgradedVersion = ext.InstalledVersion
			break
		}
	}
	require.Equal(t, targetVersion, downgradedVersion, "Extension should be downgraded to %s", targetVersion)
	t.Logf("Successfully downgraded to version: %s", downgradedVersion)

	// Step 5: Test that --force also works for reinstalling the same version
	t.Logf("Testing reinstall of same version (%s) with --force", targetVersion)

	// Get the extension binary path before deletion
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	extPath := filepath.Join(homeDir, ".azd", "extensions", "microsoft.azd.demo")

	// Delete the extension files but keep the metadata
	t.Log("Deleting extension files to simulate corruption")
	err = removeAllWithDiagnostics(t, extPath)
	require.NoError(t, err)

	// Try to install without --force (should skip)
	t.Log("Attempting install without --force (should skip)")
	result, err = cli.RunCommand(ctx, "ext", "install", "microsoft.azd.demo", "-s", "test-local", "-v", targetVersion)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(result.Stdout), "skipped", "Should skip installation without --force")

	// Verify files are still missing
	_, err = os.Stat(extPath)
	require.True(t, os.IsNotExist(err), "Extension files should still be missing after skipped install")

	// Now install with --force (should reinstall)
	t.Log("Attempting install with --force (should reinstall)")
	result, err = cli.RunCommand(
		ctx, "ext", "install", "microsoft.azd.demo", "-s", "test-local", "-v", targetVersion, "--force")
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(result.Stdout), "skipped", "Should not skip installation with --force")

	// Verify files are restored
	_, err = os.Stat(extPath)
	require.NoError(t, err, "Extension files should be restored after --force install")

	t.Log("Successfully verified --force flag behavior for reinstalling same version")
}

// Test_CLI_Extension_Capabilities tests the extension framework capabilities using the demo extension.
// This test verifies that:
// 1. The demo extension can be built and installed
// 2. The extension-capabilities sample project can run through the full azd up workflow
// 3. The extension's framework service manager handles restore, build, package operations correctly
func Test_CLI_Extension_Capabilities(t *testing.T) {
	// Skip on Windows and macOS: Docker configuration differences
	// Windows runs Docker in Windows container mode which requires Windows-specific base images
	// macOS and Windows CI environments may have Docker configuration incompatibilities
	// This test is designed for Linux environments only
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skipf("Skipping test on %s - only supported on Linux", runtime.GOOS)
	}

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
