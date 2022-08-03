package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func upCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		commands.CompositeAction(
			&ignoreInitErrorAction{
				action: &initAction{
					rootOptions: rootOptions,
				},
			},
			&infraCreateAction{
				rootOptions: rootOptions,
			},
			// Print an additional newline to separate provision from deploy
			commands.ActionFunc(
				func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
					formatter, err := output.GetFormatter(cmd)
					if err != nil {
						return err
					}

					interactive := formatter.Kind() == output.NoneFormat
					if interactive {
						fmt.Println()
					}

					return nil
				},
			),
			&deployAction{rootOptions: rootOptions},
		),
		rootOptions,
		"up",
		"Initialize application, provision Azure resources, and deploy your project with a single command.",
		`Initialize the project (if the project folder has not been initialized or cloned from a template), provision Azure resources, and deploy your project with a single command.

This command executes the following in one step:

	$ azd init
	$ azd provision
	$ azd deploy

When no template is supplied, you can optionally select an Azure Developer CLI template for cloning. Otherwise, running `+withBackticks("azd up")+` initializes the current directory so that your project is compatible with Azure Developer CLI.`,
	)

	output.AddOutputParam(cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)

	return cmd
}

type ignoreInitErrorAction struct {
	action commands.Action
}

func (a *ignoreInitErrorAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
	err := a.action.Run(ctx, cmd, args, azdCtx)
	var envInitError *environment.EnvironmentInitError
	if errors.As(err, &envInitError) {
		// We can ignore environment already initialized errors
		return nil
	} else if err != nil {
		return fmt.Errorf("running init: %w", err)
	}

	return nil
}

func (a *ignoreInitErrorAction) SetupFlags(persistent *pflag.FlagSet, local *pflag.FlagSet) {
	a.action.SetupFlags(persistent, local)
}
