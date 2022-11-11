package snapshottest

import (
	"log"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/spf13/cobra"
)

func TestCommandSnapshot(t *testing.T) {
	root := cmd.NewRootCmd()

	testCmdSnapshot(t, root)
}

func testCmdSnapshot(t *testing.T, cmd *cobra.Command) {
	t.Run(cmd.Name(), func(t *testing.T) {
		log.Printf("Command: %s", cmd.CommandPath())

		cmd.InitDefaultHelpCmd()
		cmd.InitDefaultHelpFlag()
		snaps.MatchSnapshot(t, cmd.UsageString())

		for _, c := range cmd.Commands() {
			if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
				continue
			}

			testCmdSnapshot(t, c)
		}
	})
}
