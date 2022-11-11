package cmd

import (
	"log"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/spf13/cobra"
)

func TestUsage(t *testing.T) {
	root := NewRootCmd()

	usageSnapshot(t, root)
}

func usageSnapshot(t *testing.T, cmd *cobra.Command) {
	t.Run(cmd.Name(), func(t *testing.T) {
		log.Printf("Command: %s", cmd.CommandPath())
		snaps.MatchSnapshot(t, cmd.UsageString())

		for _, c := range cmd.Commands() {
			if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
				continue
			}

			usageSnapshot(t, c)
		}
	})
}
