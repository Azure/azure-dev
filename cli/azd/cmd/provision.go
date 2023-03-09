package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

type provisionFlags struct {
	infraCreateFlags
}

func newProvisionFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *provisionFlags {
	flags := &provisionFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newProvisionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "provision",
		Aliases: []string{"infra create"},
		Short:   "Provision the Azure resources for an application.",
	}
}

type provisionAction struct {
	infraCreate *infraCreateAction
}

func newProvisionAction(
	provisionFlags *provisionFlags,
	infraCreate *infraCreateAction,
) actions.Action {
	// Required to ensure the sub action flags are bound correctly to the actions
	infraCreate.flags = &provisionFlags.infraCreateFlags

	return &provisionAction{
		infraCreate: infraCreate,
	}
}

func (a *provisionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	return a.infraCreate.Run(ctx)
}

func getCmdProvisionHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(fmt.Sprintf(
		"Provision the Azure resources for an application."+
			" This step may take a while depending on the resources provisioned."+
			" You should run %s any time you update your Bicep or Terraform file."+
			"\n\nThis command prompts you to input the following:",
		output.WithHighLightFormat(c.CommandPath())), []string{
		formatHelpNote("Environment name: The name of your environment (ex: dev, test, prod)."),
		formatHelpNote("Azure location: The Azure location where your resources will be deployed."),
		formatHelpNote("Azure subscription: The Azure subscription where your resources will be deployed."),
	})
}
