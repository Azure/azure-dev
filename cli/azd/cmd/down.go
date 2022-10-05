package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/spf13/cobra"
)

func downCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *infraDeleteFlags) {
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Delete Azure resources for an application.",
	}

	idf := &infraDeleteFlags{}
	idf.Setup(cmd.Flags(), global)

	return cmd, idf
}
