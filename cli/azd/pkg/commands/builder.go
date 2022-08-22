package commands

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
)

// Build builds a Cobra command, attaching an action
func Build(action Action, rootOptions *GlobalCommandOptions, use string, short string, long string) *cobra.Command {
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
			ctx = WithGlobalCommandOptions(ctx, rootOptions)

			// Create and set the AzCli that will be used for the command
			azCli := GetAzCliFromContext(ctx)
			ctx = azcli.WithAzCli(ctx, azCli)

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
