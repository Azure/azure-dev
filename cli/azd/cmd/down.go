package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func downCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&infraDeleteAction{
			rootOptions: rootOptions,
		},
		commands.BuildOptions{
			GlobalOptions: rootOptions,
			Use:           "down",
			Short:         "Delete Azure resources for an application.",
			Long:          "",
		},
	)

	output.AddOutputParam(cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)

	return cmd
}
