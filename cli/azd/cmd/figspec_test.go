// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/stretchr/testify/require"
)

// TestFigSpec generates a Fig autocomplete spec for azd, powering VS Code terminal IntelliSense.
// The generated TypeScript spec must be committed to the vscode repository to enable completions.
//
// To update snapshots (assuming your current directory is cli/azd):
//
// For Bash,
// UPDATE_SNAPSHOTS=true go test ./cmd -run TestFigSpec
//
// For Pwsh,
// $env:UPDATE_SNAPSHOTS='true'; go test ./cmd -run TestFigSpec; $env:UPDATE_SNAPSHOTS=$null
func TestFigSpec(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")

	cli := azdcli.NewCLI(t)

	sourceName := addLocalRegistrySource(t.Context(), t, cli)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()
		removeLocalExtensionSource(ctx, t, cli)
	})

	installAllExtensions(t.Context(), t, cli, sourceName)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()
		uninstallAllExtensions(ctx, t, cli)
	})

	// Generate the Fig spec using CLI command
	result, err := cli.RunCommand(t.Context(), "completion", "fig")
	require.NoError(t, err)

	snapshotter := snapshot.NewConfig(".ts")
	snapshotter.SnapshotT(t, result.Stdout)
}
