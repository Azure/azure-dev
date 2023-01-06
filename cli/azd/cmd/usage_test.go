package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/spf13/cobra"
)

// To update snapshots (assuming your current directory is cli/azd):
//
// For Bash,
// UPDATE_SNAPSHOTS=true go test ./cmd
//
// For Pwsh,
// $env:UPDATE_SNAPSHOTS='true'; go test ./cmd; $env:UPDATE_SNAPSHOTS=$null
func TestUsage(t *testing.T) {
	root := NewRootCmd(false, nil)

	usageSnapshot(t, root)
}

func usageSnapshot(t *testing.T, cmd *cobra.Command) {
	t.Run(cmd.Name(), func(t *testing.T) {
		snapshot.SnapshotT(t, cmd.UsageString())

		for _, c := range cmd.Commands() {
			if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
				continue
			}

			usageSnapshot(t, c)
		}
	})
}
