// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

const (
	commandTimeout  = 20 * time.Minute
	localSourceName = "figspec-local"
)

// extensionListEntry represents an extension entry returned from the extension list command.
type extensionListEntry struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

func installRegistryExtensions(t *testing.T, cli *azdcli.CLI) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	result, err := cli.RunCommand(ctx, "extension", "list", "--source", localSourceName, "--output", "json")
	require.NoError(t, err, "failed to list extensions from registry")

	var extensions []extensionListEntry
	err = json.Unmarshal([]byte(result.Stdout), &extensions)
	require.NoError(t, err, "failed to unmarshal extension list")
	require.NotEmpty(t, extensions, "extension registry returned no entries")

	for _, ext := range extensions {
		args := []string{"extension", "install", ext.ID, "--source", localSourceName}
		if ext.Version != "" {
			args = append(args, "--version", ext.Version)
		}

		t.Logf("Installing extension %s@%s", ext.ID, ext.Version)
		_, err := cli.RunCommand(ctx, args...)
		require.NoErrorf(t, err, "failed to install extension %s", ext.ID)
	}
}

func uninstallAllExtensions(t *testing.T, cli *azdcli.CLI) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	t.Log("Uninstalling all extensions")
	if _, err := cli.RunCommand(ctx, "extension", "uninstall", "--all"); err != nil {
		t.Logf("warning: failed to uninstall extensions: %v", err)
	}
}

func addLocalRegistrySource(t *testing.T, cli *azdcli.CLI) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	registryPath := filepath.Join(azdcli.GetSourcePath(), "extensions", "registry.json")
	t.Logf("Adding local registry source '%s' from %s", localSourceName, registryPath)
	_, err := cli.RunCommand(
		ctx,
		"extension", "source", "add",
		"-n", localSourceName,
		"-t", "file",
		"-l", registryPath,
	)
	require.NoError(t, err, "failed to add local registry source")
}

func removeLocalExtensionSource(t *testing.T, cli *azdcli.CLI) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	if _, err := cli.RunCommand(ctx, "extension", "source", "remove", localSourceName); err != nil {
		t.Logf("warning: failed to remove extension source %s: %v", localSourceName, err)
	}
}
