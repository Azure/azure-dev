package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type upFlags struct {
	initFlags
	infraCreateFlags
	deployFlags
	global *internal.GlobalCommandOptions
	envFlag
}

func (u *upFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	u.envFlag.Bind(local, global)
	u.global = global

	u.initFlags.bindNonCommon(local, global)
	u.initFlags.setCommon(&u.envFlag)
	u.infraCreateFlags.bindNonCommon(local, global)
	u.infraCreateFlags.setCommon(&u.envFlag)
	u.deployFlags.bindNonCommon(local, global)
	u.deployFlags.setCommon(&u.envFlag)
}

func newUpFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *upFlags {
	flags := &upFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newUpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Initialize the app, provision Azure resources, and deploy your project with a single command.",
		//nolint:lll
		Long: `Initialize the project (if the project folder has not been initialized or cloned from a template), provision Azure resources, and deploy your project with a single command.

This command executes the following in one step:

	$ azd init
	$ azd provision
	$ azd deploy

When no template is supplied, you can optionally select an Azure Developer CLI template for cloning. Otherwise, running ` + output.WithBackticks(
			"azd up",
		) + ` initializes the current directory so that your project is compatible with Azure Developer CLI.`,
	}

	return cmd
}

type upAction struct {
	flags                        *upFlags
	initActionInitializer        actions.ActionInitializer[*initAction]
	infraCreateActionInitializer actions.ActionInitializer[*infraCreateAction]
	deployActionInitializer      actions.ActionInitializer[*deployAction]
	console                      input.Console
	runner                       middleware.MiddlewareContext
}

func newUpAction(
	flags *upFlags,
	initActionInitializer actions.ActionInitializer[*initAction],
	infraCreateActionInitializer actions.ActionInitializer[*infraCreateAction],
	deployActionInitializer actions.ActionInitializer[*deployAction],
	console input.Console,
	runner middleware.MiddlewareContext,
) actions.Action {
	return &upAction{
		flags:                        flags,
		initActionInitializer:        initActionInitializer,
		infraCreateActionInitializer: infraCreateActionInitializer,
		deployActionInitializer:      deployActionInitializer,
		console:                      console,
		runner:                       runner,
	}
}

func (u *upAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	err := u.runInit(ctx)
	if err != nil {
		return nil, fmt.Errorf("running init: %w", err)
	}

	infraCreateAction := u.infraCreateActionInitializer()
	infraCreateAction.flags = &u.flags.infraCreateFlags
	provisionOptions := &middleware.Options{Name: "infracreate", Aliases: []string{"provision"}}
	_, err = u.runner.RunChildAction(ctx, provisionOptions, infraCreateAction)
	if err != nil {
		return nil, err
	}

	// Print an additional newline to separate provision from deploy
	u.console.Message(ctx, "")

	deployAction := u.deployActionInitializer()
	deployAction.flags = &u.flags.deployFlags
	deployOptions := &middleware.Options{Name: "deploy"}
	deployResult, err := u.runner.RunChildAction(ctx, deployOptions, deployAction)
	if err != nil {
		return nil, err
	}

	return deployResult, nil
}

func (u *upAction) runInit(ctx context.Context) error {
	initAction := u.initActionInitializer()
	initAction.flags = &u.flags.initFlags
	initOptions := &middleware.Options{Name: "init"}
	_, err := u.runner.RunChildAction(ctx, initOptions, initAction)
	var envInitError *environment.EnvironmentInitError
	if errors.As(err, &envInitError) {
		// We can ignore environment already initialized errors
		return nil
	}

	return err
}
