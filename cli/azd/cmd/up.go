package cmd

import (
	"context"
	"errors"
	"fmt"

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
}

func (u *upFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	output.AddOutputFlag(
		local,
		&u.outputFormat,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat)
	u.infraCreateFlags.outputFormat = &u.outputFormat
	u.deployFlags.outputFormat = &u.outputFormat

	u.initFlags.Bind(local, global)
	u.infraCreateFlags.bindWithoutOutput(local, global)
	u.deployFlags.bindWithoutOutput(local, global)

	u.global = global
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

func (i *upAction) PostRun(ctx context.Context, RunResult error) error {
	return nil
}

func (u *upAction) Run(ctx context.Context) error {
	err := u.runInit(ctx)
	if err != nil {
		return fmt.Errorf("running init: %w", err)
	}

	finalOutput := []string{}
	u.infraCreate.finalOutputRedirect = &finalOutput
	err = u.infraCreate.Run(ctx)
	if err != nil {
		return err
	}

	// Print an additional newline to separate provision from deploy
	u.console.Message(ctx, "")

	err = u.deploy.Run(ctx)
	if err != nil {
		return err
	}

	for _, message := range finalOutput {
		u.console.Message(ctx, message)
	}

	return nil
}

func (u *upAction) runInit(ctx context.Context) error {
	err := u.init.Run(ctx)
	var envInitError *environment.EnvironmentInitError
	if errors.As(err, &envInitError) {
		// We can ignore environment already initialized errors
		return nil
	}

	return err
}
