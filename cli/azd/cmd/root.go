// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	// Importing for infrastructure provider plugin registrations
	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/terraform"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"
)

func NewRootCmd(platformAgnosticHelp bool) *cobra.Command {
	prevDir := ""
	opts := &internal.GlobalCommandOptions{}

	cmd := &cobra.Command{
		Use:   "azd",
		Short: "Azure Developer CLI is a command-line interface for developers who build Azure solutions.",
		//nolint:lll
		Long: `Azure Developer CLI is a command-line interface for developers who build Azure solutions.

To begin working with Azure Developer CLI, run the ` + output.WithBackticks("azd up") + ` command by supplying a sample template in an empty directory:

	$ azd up –-template todo-nodejs-mongo

You can pick a template by running ` + output.WithBackticks("azd template list") + `and then supplying the repo name as a value to ` + output.WithBackticks("--template") + `.

The most common next commands are:

	$ azd pipeline config
	$ azd deploy
	$ azd monitor --overview

For more information, visit the Azure Developer CLI Dev Hub: https://aka.ms/azure-dev/devhub.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if opts.Cwd != "" {
				current, err := os.Getwd()

				if err != nil {
					return err
				}

				prevDir = current

				if err := os.Chdir(opts.Cwd); err != nil {
					return fmt.Errorf("failed to change directory to %s: %w", opts.Cwd, err)
				}
			}

			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			// This is just for cleanliness and making writing tests simpler since
			// we can just remove the entire project folder afterwards.
			// In practical execution, this wouldn't affect much, since the CLI is exiting.
			if prevDir != "" {
				return os.Chdir(prevDir)
			}

			return nil
		},
		SilenceUsage: true,
	}

	cmd.DisableAutoGenTag = true
	cmd.CompletionOptions.HiddenDefaultCmd = true
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	cmd.PersistentFlags().StringVarP(&opts.Cwd, "cwd", "C", "", "Sets the current working directory.")
	cmd.PersistentFlags().BoolVar(&opts.EnableDebugLogging, "debug", false, "Enables debugging and diagnostics logging.")
	cmd.PersistentFlags().
		BoolVar(
			&opts.NoPrompt,
			"no-prompt",
			false,
			"Accepts the default value instead of prompting, or it fails if there is no default.")
	cmd.SetHelpTemplate(
		fmt.Sprintf("%s\nPlease let us know how we are doing: https://aka.ms/azure-dev/hats\n", cmd.HelpTemplate()),
	)

	opts.EnableTelemetry = telemetry.IsTelemetryEnabled()

	cmd.AddCommand(configCmd(opts, platformAgnosticHelp)) // Dynamic
	cmd.AddCommand(envCmd(opts))                          // Static
	cmd.AddCommand(infraCmd(opts))                        // Static
	cmd.AddCommand(pipelineCmd(opts))                     // Static
	cmd.AddCommand(telemetryCmd(opts))                    // Static
	cmd.AddCommand(templatesCmd(opts))                    // Static
	cmd.AddCommand(authCmd(opts))                         // Static

	cmd.AddCommand(BuildCmd(opts, versionCmdDesign, initVersionAction, &buildOptions{disableTelemetry: true}))
	cmd.AddCommand(BuildCmd(opts, showCmdDesign, initShowAction, nil))
	cmd.AddCommand(BuildCmd(opts, restoreCmdDesign, initRestoreAction, nil))
	cmd.AddCommand(BuildCmd(opts, loginCmdDesign, initLoginAction, nil))
	cmd.AddCommand(BuildCmd(opts, logoutCmdDesign, initLogoutAction, nil))
	cmd.AddCommand(BuildCmd(opts, monitorCmdDesign, initMonitorAction, nil))
	cmd.AddCommand(BuildCmd(opts, downCmdDesign, initInfraDeleteAction, nil))
	cmd.AddCommand(BuildCmd(opts, initCmdDesign, initInitAction, nil)) // Static with formatting functions
	cmd.AddCommand(BuildCmd(opts, upCmdDesign, initUpAction, nil))     // Static with formatting functions
	cmd.AddCommand(BuildCmd(opts, provisionCmdDesign, initInfraCreateAction, nil))
	cmd.AddCommand(BuildCmd(opts, deployCmdDesign, initDeployAction, nil))

	return cmd
}

type designBuilder[F any] func(opts *internal.GlobalCommandOptions) (*cobra.Command, *F)

type actionBuilder[F any] func(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags F,
	args []string) (actions.Action, error)

type buildOptions struct {
	disableTelemetry bool
}

func BuildCmd[F any](
	opts *internal.GlobalCommandOptions,
	buildDesign designBuilder[F],
	buildAction actionBuilder[F],
	buildOptions *buildOptions) *cobra.Command {
	cmd, flags := buildDesign(opts)
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))

	runCmd := func(cmd *cobra.Command, ctx context.Context, args []string) error {
		console, err := initConsole(cmd, opts)
		if err != nil {
			return err
		}

		action, err := buildAction(console, ctx, opts, *flags, args)
		if err != nil {
			return err
		}

		ctx = tools.WithInstalledCheckCache(ctx)

		actionResult, err := action.Run(ctx)
		// At this point, we know that there might be an error, so we can silence cobra from showing it after us.
		cmd.SilenceErrors = true
		console.MessageUxItem(ctx, actions.ToActionResult(actionResult, err))

		return err
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if buildOptions != nil && buildOptions.disableTelemetry {
			return runCmd(cmd, cmd.Context(), args)
		} else {
			// Bind cmd, args. Only a different context needs to be passed.
			runWithContext := func(ctx context.Context) error { return runCmd(cmd, ctx, args) }
			return runCmdWithTelemetry(cmd, runWithContext)
		}
	}

	return cmd
}

func runCmdWithTelemetry(cmd *cobra.Command, runCmd func(ctx context.Context) error) error {
	// Note: CommandPath is constructed using the Use member on each command up to the root.
	// It does not contain user input, and is safe for telemetry emission.
	spanCtx, span := telemetry.GetTracer().Start(cmd.Context(), events.GetCommandEventName(cmd.CommandPath()))
	defer span.End()

	err := runCmd(spanCtx)
	if err != nil {
		span.SetStatus(codes.Error, "UnknownError")
	}

	return err

}
