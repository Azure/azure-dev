package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/spf13/cobra"
)

func provisionCmdDesign(rootOptions *internal.GlobalCommandOptions) (*cobra.Command, *infraCreateFlags) {
	cmd := &cobra.Command{
		Use:   "provision",
		Short: "Provision the Azure resources for an application.",
		//nolint:lll
		Long: `Provision the Azure resources for an application.

The command prompts you for the following:
- Environment name: The name of your environment.
- Azure location: The Azure location where your resources will be deployed.
- Azure subscription: The Azure subscription where your resources will be deployed.

Depending on what Azure resources are created, running this command might take a while. To view progress, go to the Azure portal and search for the resource group that contains your environment name.`,
	}

	f := &infraCreateFlags{}
	f.Bind(cmd.Flags(), rootOptions)

	return cmd, f
}
