package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"
)

type BuildOptions struct {
	// Global options that all commands inherit from rootCmd.
	GlobalOptions *internal.GlobalCommandOptions

	// Use is the one-line usage message.
	// Recommended syntax is as follow:
	//   [ ] identifies an optional argument. Arguments that are not enclosed in brackets are required.
	//   ... indicates that you can specify multiple values for the previous argument.
	//   |   indicates mutually exclusive information. You can use the argument to the left of the separator or the
	//       argument to the right of the separator. You cannot use both arguments in a single use of the command.
	//   { } delimits a set of mutually exclusive arguments when one of the arguments is required. If the arguments are
	//       optional, they are enclosed in brackets ([ ]).
	// Example: add [-F file | -D dir]... [-f format] profile
	Use string

	// Short is the short description shown in the 'help' output.
	Short string

	// Long is the long message shown in the 'help <this-command>' output.
	Long string

	// Disables the telemetry that tracks the command invocation.
	DisableCommandEventTelemetry bool
}

// Build builds a Cobra command, attaching an action
// All command should be built with this command builder vs manually instantiating cobra commands.
func Build(action Action, buildOptions BuildOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   buildOptions.Use,
		Short: buildOptions.Short,
		Long:  buildOptions.Long,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, azdCtx, err := createRootContext(context.Background(), cmd, buildOptions.GlobalOptions)
			if err != nil {
				return err
			}

			// Note: CommandPath is constructed using the Use member on each command up to the root.
			// It does not contain user input, and is safe for telemetry emission.
			cmdPath := cmd.CommandPath()
			ctx, span := telemetry.GetTracer().Start(ctx, events.GetCommandEventName(cmdPath))
			defer span.End()

			// inner trace
			ctx, inner := telemetry.GetTracer().Start(ctx, "azure-dev.commands.inner."+use)
			time.Sleep(time.Duration(200) * time.Millisecond)
			inner.End()

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

// Create the core context for use in all Azd commands
// Registers context values for azCli, formatter, writer, console and more.
func createRootContext(ctx context.Context, cmd *cobra.Command, rootOptions *internal.GlobalCommandOptions) (context.Context, *azdcontext.AzdContext, error) {
	azdCtx, err := azdcontext.NewAzdContext()
	if err != nil {
		return ctx, nil, fmt.Errorf("creating context: %w", err)
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
		return ctx, nil, err
	}

	if formatter != nil {
		ctx = output.WithFormatter(ctx, formatter)
	}

	writer := output.GetDefaultWriter()
	ctx = output.WithWriter(ctx, writer)

	console := input.NewConsole(!rootOptions.NoPrompt, writer, formatter)
	ctx = input.WithConsole(ctx, console)

	return ctx, azdCtx, nil
}
