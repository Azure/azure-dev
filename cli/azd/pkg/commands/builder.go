package commands

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/terraform"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"
)

// BuildOptions contains the optional parameters for the Build function.
type BuildOptions struct {
	// Long is the long message shown in the 'help <this-command>' output. If Long is not provided, the Short message is used instead.
	Long string

	// Aliases is an array of aliases that can be used instead of the first word in Use.
	Aliases []string

	// Disables the usage event telemetry associated to the command.
	DisableCmdUsageEvent bool
}

// Build builds a Cobra command, attaching an action.
//
// All commands should be built with this command builder vs manually instantiating cobra commands.
//
// Use is the one-line usage message.
// Recommended syntax is as follow:
//
//	[ ] identifies an optional argument. Arguments that are not enclosed in brackets are required.
//	... indicates that you can specify multiple values for the previous argument.
//	|   indicates mutually exclusive information. You can use the argument to the left of the separator or the
//	    argument to the right of the separator. You cannot use both arguments in a single use of the command.
//	{ } delimits a set of mutually exclusive arguments when one of the arguments is required. If the arguments are
//	    optional, they are enclosed in brackets ([ ]).
//
// Example: add [-F file | -D dir]... [-f format] profile
func Build(action Action, rootOptions *internal.GlobalCommandOptions, use string, short string, buildOptions *BuildOptions) *cobra.Command {
	if buildOptions == nil {
		buildOptions = &BuildOptions{}
	}

	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    buildOptions.Long,
		Aliases: buildOptions.Aliases,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, azdCtx, err := createRootContext(cmd.Context(), cmd, rootOptions)
			if err != nil {
				return err
			}

			runCmd := func(cmdCtx context.Context) error {
				return action.Run(cmdCtx, cmd, args, azdCtx)
			}

			if buildOptions.DisableCmdUsageEvent {
				return runCmd(ctx)
			} else {
				return runCmdWithTelemetry(ctx, cmd, runCmd)
			}
		},
	}
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	action.SetupFlags(
		cmd.PersistentFlags(),
		cmd.Flags(),
	)
	return cmd
}

func runCmdWithTelemetry(ctx context.Context, cmd *cobra.Command, runCmd func(ctx context.Context) error) error {
	// Note: CommandPath is constructed using the Use member on each command up to the root.
	// It does not contain user input, and is safe for telemetry emission.
	spanCtx, span := telemetry.GetTracer().Start(ctx, events.GetCommandEventName(cmd.CommandPath()))
	defer span.End()

	err := runCmd(spanCtx)
	if err != nil {
		span.SetStatus(codes.Error, "UnknownError")
	}

	return err
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

	// Set default credentials used for operations against azure data/control planes
	credentials, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		panic("failed creating azure cli credential")
	}
	ctx = identity.WithCredentials(ctx, credentials)

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
