package commands

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
)

// Build builds a Cobra command, attaching an action
// All command should be built with this command builder vs manually instantiating cobra commands.
func Build(action Action, rootOptions *internal.GlobalCommandOptions, use string, short string, long string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			azdCtx, err := azdcontext.NewAzdContext()
			if err != nil {
				return fmt.Errorf("creating context: %w", err)
			}

			// Set the global options in the go context
			ctx = azdcontext.WithAzdContext(ctx, azdCtx)
			ctx = internal.WithCommandOptions(ctx, *rootOptions)

			azCliArgs := azcli.NewAzCliArgs{
				EnableDebug:     rootOptions.EnableDebugLogging,
				EnableTelemetry: rootOptions.EnableTelemetry,
			}

			// Create and set the AzCli that will be used for the command
			azCli := azcli.NewAzCli(azCliArgs)
			ctx = azcli.WithAzCli(ctx, azCli)

			// Attempt to get the user specified formatter from the command args
			formatter, err := output.GetCommandFormatter(cmd)
			if err != nil {
				return err
			}

			ctx = output.WithFormatter(ctx, formatter)

			writer := output.GetDefaultWriter()
			ctx = output.WithWriter(ctx, writer)

			console := input.NewConsole(!rootOptions.NoPrompt, writer, formatter)
			ctx = input.WithConsole(ctx, console)

			return action.Run(ctx, cmd, args, azdCtx)
		},
	}
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	action.SetupFlags(
		cmd.PersistentFlags(),
		cmd.Flags(),
	)
	return cmd
}
