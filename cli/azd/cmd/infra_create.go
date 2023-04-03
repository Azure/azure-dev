package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

type infraCreateFlags struct {
	provisionFlags
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
	infraCreate *provisionAction
	console     input.Console
}

func newInfraCreateAction(
	createFlags *infraCreateFlags,
	provision *provisionAction,
	console input.Console,
) actions.Action {
	// Required to ensure the sub action flags are bound correctly to the actions
	provision.flags = &createFlags.provisionFlags

	return &infraCreateAction{
		infraCreate: provision,
		console:     console,
	}
}

func (a *infraCreateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	fmt.Fprintln(
		a.console.Handles().Stderr,
		output.WithWarningFormat(
			"`azd infra create` is deprecated and will be removed in a future release. Please use `azd provision` instead."),
	)
	return a.infraCreate.Run(ctx)
}
