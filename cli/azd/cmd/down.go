package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/spf13/cobra"
)

type downFlags struct {
	infraDeleteFlags
}

func newDownFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *downFlags {
	flags := &downFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Delete Azure resources for an application.",
	}
}
