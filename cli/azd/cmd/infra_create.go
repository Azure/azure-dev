package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

type infraCreateFlags struct {
	cmd.ProvisionFlags
}

func newInfraCreateFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *infraCreateFlags {
	flags := &infraCreateFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newInfraCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "create",
		Short:  "Provision the Azure resources for an application.",
		Hidden: true,
	}
}

type infraCreateAction struct {
	infraCreate *cmd.ProvisionAction
	console     input.Console
}

func newInfraCreateAction(
	createFlags *infraCreateFlags,
	provision *cmd.ProvisionAction,
	console input.Console,
) actions.Action {
	// Required to ensure the sub action flags are bound correctly to the actions
	provision.SetFlags(&createFlags.ProvisionFlags)

	return &infraCreateAction{
		infraCreate: provision,
		console:     console,
	}
}

func (a *infraCreateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	fmt.Fprintln(
		a.console.Handles().Stderr,
		output.WithWarningFormat(
			"WARNING: `azd infra create` is deprecated and will be removed in a future release."))
	fmt.Fprintln(
		a.console.Handles().Stderr,
		"Next time use `azd provision`")
	return a.infraCreate.Run(ctx)
}
