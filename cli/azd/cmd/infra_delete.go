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

type infraDeleteFlags struct {
	downFlags
}

func newInfraDeleteFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *infraDeleteFlags {
	flags := &infraDeleteFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newInfraDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "delete",
		Short:  "Delete Azure resources for an application.",
		Hidden: true,
	}
}

type infraDeleteAction struct {
	down    *downAction
	console input.Console
}

func newInfraDeleteAction(
	deleteFlags *infraDeleteFlags,
	down *downAction,
	console input.Console,
) actions.Action {
	// Required to ensure the sub action flags are bound correctly to the actions
	down.flags = &deleteFlags.downFlags

	return &infraDeleteAction{
		down:    down,
		console: console,
	}
}

func (a *infraDeleteAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	fmt.Fprintln(
		a.console.Handles().Stderr,
		output.WithWarningFormat(
			"WARNING: `azd infra delete` is deprecated and will be removed in a future release."))
	fmt.Fprintln(
		a.console.Handles().Stderr,
		"Next time use `azd down`")
	return a.down.Run(ctx)
}
