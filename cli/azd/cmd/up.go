package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
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
	outputFormat string
	global       *internal.GlobalCommandOptions
	envFlag
}

func (u *upFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	output.AddOutputFlag(
		local,
		&u.outputFormat,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat)

	u.envFlag.Bind(local, global)
	u.global = global

	u.initFlags.bindNonCommon(local, global)
	u.initFlags.setCommon(&u.envFlag)
	u.infraCreateFlags.bindNonCommon(local, global)
	u.infraCreateFlags.setCommon(&u.outputFormat, &u.envFlag)
	u.deployFlags.bindNonCommon(local, global)
	u.deployFlags.setCommon(&u.outputFormat, &u.envFlag)
}

func upCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *upFlags) {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Initialize application, provision Azure resources, and deploy your project with a single command.",
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

	uf := &upFlags{}
	uf.Bind(cmd.Flags(), global)

	if err := cmd.RegisterFlagCompletionFunc("template", templateNameCompletion); err != nil {
		panic(err)
	}

	return cmd, uf
}

type upAction struct {
	init        *initAction
	infraCreate *infraCreateAction
	deploy      *deployAction
	console     input.Console
}

func newUpAction(init *initAction, infraCreate *infraCreateAction, deploy *deployAction, console input.Console) *upAction {
	return &upAction{
		init:        init,
		infraCreate: infraCreate,
		deploy:      deploy,
		console:     console,
	}
}

func (u *upAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	err := u.runInit(ctx)
	if err != nil {
		return nil, fmt.Errorf("running init: %w", err)
	}

	finalOutput := []string{}
	u.infraCreate.finalOutputRedirect = &finalOutput
	_, err = u.infraCreate.Run(ctx)
	if err != nil {
		return nil, err
	}

	// Print an additional newline to separate provision from deploy
	u.console.Message(ctx, "")

	_, err = u.deploy.Run(ctx)
	if err != nil {
		return nil, err
	}

	for _, message := range finalOutput {
		u.console.Message(ctx, message)
	}

	return nil, nil
}

func (u *upAction) runInit(ctx context.Context) error {
	_, err := u.init.Run(ctx)
	var envInitError *environment.EnvironmentInitError
	if errors.As(err, &envInitError) {
		// We can ignore environment already initialized errors
		return nil
	}

	return err
}
