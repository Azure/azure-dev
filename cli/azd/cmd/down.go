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
	runner      middleware.MiddlewareContext
	infraDelete infraDeleteAction
}

func newDownAction(
	runner middleware.MiddlewareContext,
	downFlags *downFlags,
	infraDelete *infraDeleteAction,
) actions.Action {
	// Required to ensure the sub action flags are bound correctly to the actions
	infraDelete.flags = &downFlags.infraDeleteFlags

	return &downAction{
		infraDelete: *infraDelete,
		runner:      runner,
	}
}

func (a *downAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	runOptions := &middleware.Options{Name: "infradelete"}
	return a.runner.RunChildAction(ctx, runOptions, &a.infraDelete)
}
