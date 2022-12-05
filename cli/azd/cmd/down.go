package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func downCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *infraDeleteFlags) {
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Delete Azure resources for an application.",
	}

	idf := &infraDeleteFlags{}
	idf.Bind(cmd.Flags(), global)

	output.AddOutputParam(cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)

	return cmd, idf
}
