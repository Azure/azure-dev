package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/spf13/cobra"
)

type downFlags struct {
	infraDeleteFlags
}

func newDownFlags(cmd *cobra.Command, infraDeleteFlags *infraDeleteFlags, global *internal.GlobalCommandOptions) *downFlags {
	flags := &downFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Delete Azure resources for an app.",
	}
}

type downAction struct {
	flags                  *downFlags
	runner                 middleware.MiddlewareContext
	infraDeleteInitializer actions.ActionInitializer[*infraDeleteAction]
}

func newDownAction(
	runner middleware.MiddlewareContext,
	downFlags *downFlags,
	infraDeleteInitializer actions.ActionInitializer[*infraDeleteAction],
) actions.Action {
	return &downAction{
		flags:                  downFlags,
		infraDeleteInitializer: infraDeleteInitializer,
		runner:                 runner,
	}
}

func (a *downAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	infraDeleteAction := a.infraDeleteInitializer()
	infraDeleteAction.flags = &a.flags.infraDeleteFlags
	runOptions := &middleware.Options{Name: "infradelete"}
	return a.runner.RunChildAction(ctx, runOptions, infraDeleteAction)
}
