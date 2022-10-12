// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/cmd/action"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"
)

func NewRootCmd() *cobra.Command {
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

			if opts.EnvironmentName == "" {
				opts.EnvironmentName = os.Getenv(environment.EnvNameEnvVarName)
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
	cmd.PersistentFlags().StringVarP(&opts.EnvironmentName, "environment", "e", "", "The name of the environment to use.")
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

	cmd.AddCommand(envCmd(opts))
	cmd.AddCommand(infraCmd(opts))
	cmd.AddCommand(pipelineCmd(opts))
	cmd.AddCommand(telemetryCmd(opts))
	cmd.AddCommand(templatesCmd(opts))

	cmd.AddCommand(BuildCmd(opts, versionCmdDesign, initVersionAction, &buildOptions{disableTelemetry: true}))
	cmd.AddCommand(BuildCmd(opts, showCmdDesign, initShowAction, nil))
	cmd.AddCommand(BuildCmd(opts, restoreCmdDesign, initRestoreAction, nil))
	cmd.AddCommand(BuildCmd(opts, loginCmdDesign, initLoginAction, nil))
	cmd.AddCommand(BuildCmd(opts, monitorCmdDesign, initMonitorAction, nil))
	cmd.AddCommand(BuildCmd(opts, downCmdDesign, initInfraDeleteAction, nil))
	cmd.AddCommand(BuildCmd(opts, initCmdDesign, initInitAction, nil))
	cmd.AddCommand(BuildCmd(opts, upCmdDesign, initUpAction, nil))
	cmd.AddCommand(BuildCmd(opts, provisionCmdDesign, initInfraCreateAction, nil))
	cmd.AddCommand(BuildCmd(opts, deployCmdDesign, initDeployAction, nil))

	return cmd
}

type designBuilder[F any] func(opts *internal.GlobalCommandOptions) (*cobra.Command, *F)
type actionBuilder[F any] func(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags F, args []string) (action.Action, error)
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

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		action, err := buildAction(cmd, opts, *flags, args)
		if err != nil {
			return err
		}

		// TODO: Is this needed? SHIM to maintain backwards compatibility
		ctx := context.Background()
		ctx, err = commands.RegisterDependenciesInCtx(ctx, cmd, opts)
		if err != nil {
			return err
		}

		runCmd := func(cmdCtx context.Context) error {
			return action.Run(cmdCtx)
		}

		if buildOptions != nil && buildOptions.disableTelemetry {
			return runCmd(ctx)
		} else {
			return runCmdWithTelemetry(ctx, cmd, runCmd)
		}
	}
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

func Execute(args []string) error {
	tempRootCmd := NewRootCmd()
	tempRootCmd.SetArgs(args)
	return tempRootCmd.Execute()
}
