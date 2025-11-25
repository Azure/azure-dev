// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

const (
	localSourceName = "local"
)

// extensionListEntry represents an extension entry returned from the extension list command.
type extensionListEntry struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

func installAllExtensions(ctx context.Context, t *testing.T, cli *azdcli.CLI, sourceName string) {
	t.Helper()

	result, err := cli.RunCommand(ctx, "extension", "list", "--source", sourceName, "--output", "json")
	require.NoError(t, err, "failed to list extensions from source %s", sourceName)

	var extensions []extensionListEntry
	err = json.Unmarshal([]byte(result.Stdout), &extensions)
	require.NoError(t, err, "failed to unmarshal extension list")

	if len(extensions) == 0 {
		t.Logf("No extensions found in source %s to install", sourceName)
		return
	}

	for _, ext := range extensions {
		args := []string{"extension", "install", ext.ID, "--source", sourceName}
		if ext.Version != "" {
			args = append(args, "--version", ext.Version)
		}

		t.Logf("Installing extension %s@%s", ext.ID, ext.Version)
		_, err := cli.RunCommand(ctx, args...)
		require.NoErrorf(t, err, "failed to install extension %s from source %s", ext.ID, sourceName)
	}
}

func uninstallAllExtensions(ctx context.Context, t *testing.T, cli *azdcli.CLI) {
	t.Helper()

	t.Log("Uninstalling all extensions")
	if _, err := cli.RunCommand(ctx, "extension", "uninstall", "--all"); err != nil {
		t.Logf("warning: failed to uninstall extensions: %v", err)
	}
}

func addLocalRegistrySource(ctx context.Context, t *testing.T, cli *azdcli.CLI) string {
	t.Helper()

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
	return localSourceName
}

func removeLocalExtensionSource(ctx context.Context, t *testing.T, cli *azdcli.CLI) {
	t.Helper()

	if _, err := cli.RunCommand(ctx, "extension", "source", "remove", localSourceName); err != nil {
		t.Logf("warning: failed to remove extension source %s: %v", localSourceName, err)
	}
}
