package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/spf13/cobra"
)

func newProvisionFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *infraCreateFlags {
	flags := &infraCreateFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newProvisionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "provision",
		Short: "Provision the Azure resources for an app.",
		//nolint:lll
		Long: `Provision the Azure resources for an app.

The command prompts you for the following values:
- Environment name: The name of your environment.
- Azure location: The Azure location where your resources will be deployed.
- Azure subscription: The Azure subscription where your resources will be deployed.

Depending on what Azure resources are created, running this command might take a while. To view progress, go to the Azure portal and search for the resource group that contains your environment name.`,
	}
}
