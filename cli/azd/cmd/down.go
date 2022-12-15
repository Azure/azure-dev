package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/spf13/cobra"
)

func newDownFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *infraDeleteFlags {
	flags := &infraDeleteFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Delete Azure resources for an application.",
	}
}
