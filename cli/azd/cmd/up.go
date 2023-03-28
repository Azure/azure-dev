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
	return &cobra.Command{
		Use:   "up",
		Short: "Initialize application, provision Azure resources, and deploy your project with a single command.",
	}
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
		return nil, err
	}

	infraCreateAction, err := u.infraCreateActionInitializer()
	if err != nil {
		return nil, err
	}

	infraCreateAction.flags = &u.flags.infraCreateFlags
	provisionOptions := &middleware.Options{CommandPath: "infra create", Aliases: []string{"provision"}}
	_, err = u.runner.RunChildAction(ctx, provisionOptions, infraCreateAction)
	if err != nil {
		return nil, err
	}

	// Print an additional newline to separate provision from deploy
	u.console.Message(ctx, "")

	deployAction, err := u.deployActionInitializer()
	if err != nil {
		return nil, err
	}

	deployAction.flags = &u.flags.deployFlags
	deployOptions := &middleware.Options{CommandPath: "deploy"}
	deployResult, err := u.runner.RunChildAction(ctx, deployOptions, deployAction)
	if err != nil {
		return nil, err
	}

	return deployResult, nil
}

func (u *upAction) runInit(ctx context.Context) error {
	initAction, err := u.initActionInitializer()
	if err != nil {
		return err
	}

	initAction.flags = &u.flags.initFlags
	initOptions := &middleware.Options{CommandPath: "init"}
	_, err = u.runner.RunChildAction(ctx, initOptions, initAction)
	var envInitError *environment.EnvironmentInitError
	if errors.As(err, &envInitError) {
		// We can ignore environment already initialized errors
		return nil
	}

	return err
}

func getCmdUpHelpDescription(c *cobra.Command) string {

	return generateCmdHelpDescription(
		fmt.Sprintf("Executes the %s, %s and %s commands in a single step.",
			output.WithHighLightFormat("azd init"),
			output.WithHighLightFormat("azd provision"),
			output.WithHighLightFormat("azd deploy")), getCmdHelpDescriptionNoteForInit(c))
}

func getCmdUpHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Initialize, provision and deploy a template to Azure from a GitHub repo.": fmt.Sprintf("%s %s",
			output.WithHighLightFormat("azd up --template"),
			output.WithWarningFormat("[GitHub repo URL]"),
		),
	})
}
