package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type upFlags struct {
	global *internal.GlobalCommandOptions
	*provisionFlags
	*deployFlags
	*envFlag
}

func (u *upFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	u.provisionFlags.setCommon(u.envFlag)
	u.deployFlags.setCommon(u.envFlag)
}

func newUpFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *upFlags {
	flags := &upFlags{
		global:         global,
		envFlag:        newEnvFlag(cmd, global),
		provisionFlags: newProvisionFlags(cmd, global),
		deployFlags:    newDeployFlags(cmd, global),
	}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Provision Azure resources, and deploy your project with a single command.",
	}
}

type upAction struct {
	flags                      *upFlags
	provisionActionInitializer actions.ActionInitializer[*provisionAction]
	deployActionInitializer    actions.ActionInitializer[*deployAction]
	console                    input.Console
	runner                     middleware.MiddlewareContext
}

func newUpAction(
	flags *upFlags,
	provisionActionInitializer actions.ActionInitializer[*provisionAction],
	deployActionInitializer actions.ActionInitializer[*deployAction],
	console input.Console,
	runner middleware.MiddlewareContext,
) actions.Action {
	return &upAction{
		flags:                      flags,
		provisionActionInitializer: provisionActionInitializer,
		deployActionInitializer:    deployActionInitializer,
		console:                    console,
		runner:                     runner,
	}
}

func (u *upAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if u.flags.provisionFlags.noProgress {
		fmt.Fprintln(
			u.console.Handles().Stderr,
			output.WithWarningFormat("The --no-progress flag is deprecated and will be removed in the future."))
		// this flag actually isn't used by the provision command, we set it to false to hide the extra warning
		u.flags.provisionFlags.noProgress = false
	}

	if u.flags.deployFlags.serviceName != "" {
		fmt.Fprintln(
			u.console.Handles().Stderr,
			output.WithWarningFormat("The --service flag is deprecated and will be removed in the future."))
	}

	provisionAction, err := u.provisionActionInitializer()
	if err != nil {
		return nil, err
	}

	provisionAction.flags = u.flags.provisionFlags
	provisionOptions := &middleware.Options{CommandPath: "infra create", Aliases: []string{"provision"}}
	_, err = u.runner.RunChildAction(ctx, provisionOptions, provisionAction)
	if err != nil {
		return nil, err
	}

	// Print an additional newline to separate provision from deploy
	u.console.Message(ctx, "")

	deployAction, err := u.deployActionInitializer()
	if err != nil {
		return nil, err
	}

	deployAction.flags = u.flags.deployFlags
	// move flag to args to avoid extra deprecation flag warning
	if deployAction.flags.serviceName != "" {
		deployAction.args = []string{deployAction.flags.serviceName}
		deployAction.flags.serviceName = ""
	}
	deployOptions := &middleware.Options{CommandPath: "deploy"}
	deployResult, err := u.runner.RunChildAction(ctx, deployOptions, deployAction)
	if err != nil {
		return nil, err
	}

	return deployResult, nil
}

func getCmdUpHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription(
		fmt.Sprintf("Executes the %s and %s commands in a single step.",
			output.WithHighLightFormat("azd provision"),
			output.WithHighLightFormat("azd deploy")), nil)
}
