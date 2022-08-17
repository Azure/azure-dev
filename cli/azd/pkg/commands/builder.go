package commands

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

// Build builds a Cobra command, attaching an action
func Build(action Action, rootOptions *GlobalCommandOptions, use string, short string, long string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			azdCtx, err := environment.NewAzdContext()
			if err != nil {
				return fmt.Errorf("creating context: %w", err)
			}

			// Set the global options in the go context
			ctx = context.WithValue(ctx, environment.AzdContextKey, azdCtx)
			ctx = context.WithValue(ctx, environment.OptionsContextKey, rootOptions)

			// Create and set the AzCli that will be used for the command
			azCli := GetAzCliFromContext(ctx)
			ctx = context.WithValue(ctx, environment.AzdCliContextKey, azCli)

			// This is done to simply mock behavior. We could either get the full command invocation path
			// using GetCommandPath, or more than likely, ask for the event name as a Builder argument
			ctx, span := otel.Tracer("azd").Start(ctx, "azure-dev.commands."+cmd.Name())
			defer span.End()

			err = action.Run(ctx, cmd, args, azdCtx)
			if err != nil {
				span.SetStatus(codes.Error, "UnknownError")
			}

			return err
		},
	}
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	action.SetupFlags(
		cmd.PersistentFlags(),
		cmd.Flags(),
	)
	return cmd
}
