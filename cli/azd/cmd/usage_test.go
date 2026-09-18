// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// To update snapshots (assuming your current directory is cli/azd):
//
// For Bash,
// UPDATE_SNAPSHOTS=true go test ./cmd
//
// For Pwsh,
// $env:UPDATE_SNAPSHOTS='true'; go test ./cmd; $env:UPDATE_SNAPSHOTS=$null
func TestUsage(t *testing.T) {
	// disable rich formatting output
	t.Setenv("TERM", "dumb")
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
	t.Setenv("AZD_SKIP_FIRST_RUN", "true")

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

	root := NewRootCmd(false, nil, nil)

	usageSnapshot(t, root)
}

func usageSnapshot(t *testing.T, cmd *cobra.Command) {
	t.Run(cmd.Name(), func(t *testing.T) {
		var result bytes.Buffer
		cmd.SetOut(&result)
		cmd.SetErr(&result)
		require.NoError(t, cmd.Help())
		snapshot.SnapshotT(t, result.String())

		for _, c := range cmd.Commands() {
			if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
				continue
			}

			usageSnapshot(t, c)
		}
	})
}
