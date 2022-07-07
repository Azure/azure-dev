package commands

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/spf13/cobra"
)

//Build builds a Cobra command, attaching an action
func Build(action Action, rootOptions *GlobalCommandOptions, use string, short string, long string) *cobra.Command {
	cobraCommand := &cobra.Command{
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

			return action.Run(ctx, cmd, args, azdCtx)
		},
	}
	action.SetupFlags(
		cobraCommand.PersistentFlags(),
		cobraCommand.Flags(),
	)
	return cobraCommand
}
