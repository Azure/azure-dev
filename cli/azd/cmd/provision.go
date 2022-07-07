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
		"Provision the Azure resources for an application",
		`Provision the Azure resources for an application

The command prompts you for the following:
	- Environment Name: Name of your environment.
	- Azure Location: The Azure location where your resources will be deployed.
	- Azure Subscription: The Azure Subscription where your resources will be deployed.
	
Depending on what Azure resources are created, this may take a while. To view progress, go to Azure portal and search for the resource group that contains your environment name.`,
	)
	cmd.Flags().BoolP("help", "h", false, "Help for "+cmd.Name())

	return output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)
}
