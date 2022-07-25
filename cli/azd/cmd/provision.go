package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func provisionCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&infraCreateAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"provision",
		"Provision the Azure resources for an application.",
		`Provision the Azure resources for an application.

The command prompts you for the following:
- Environment name: The name of your environment.
- Azure location: The Azure location where your resources will be deployed.
- Azure subscription: The Azure subscription where your resources will be deployed.

Depending on what Azure resources are created, running this command might take a while. To view progress, go to the Azure portal and search for the resource group that contains your environment name.`,
	)

	return output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)
}
