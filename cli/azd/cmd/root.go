// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/golobby/container/v3"

	// Importing for infrastructure provider plugin registrations

	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/terraform"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func NewRootCmd(staticHelp bool) *cobra.Command {
	prevDir := ""
	opts := &internal.GlobalCommandOptions{GenerateStaticHelp: staticHelp}

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

	//attachDebugger()

	cmd.AddCommand(configCmd(opts))
	cmd.AddCommand(envCmd(opts))
	cmd.AddCommand(infraCmd(opts))
	cmd.AddCommand(pipelineCmd(opts))
	cmd.AddCommand(telemetryCmd(opts))
	cmd.AddCommand(templatesCmd(opts))
	cmd.AddCommand(authCmd(opts))

	cmd.AddCommand(BuildCmd(opts, versionCmdDesign, newVersionAction, &actions.BuildOptions{DisableTelemetry: true}))
	cmd.AddCommand(BuildCmd(opts, showCmdDesign, newShowAction, nil))
	cmd.AddCommand(BuildCmd(opts, restoreCmdDesign, newRestoreAction, nil))
	cmd.AddCommand(BuildCmd(opts, loginCmdDesign, newLoginAction, nil))
	cmd.AddCommand(BuildCmd(opts, logoutCmdDesign, newLogoutAction, nil))
	cmd.AddCommand(BuildCmd(opts, monitorCmdDesign, newMonitorAction, nil))
	cmd.AddCommand(BuildCmd(opts, downCmdDesign, newInfraDeleteAction, nil))
	cmd.AddCommand(BuildCmd(opts, initCmdDesign, newInitAction, nil))
	cmd.AddCommand(BuildCmd(opts, upCmdDesign, newUpAction, nil))
	cmd.AddCommand(BuildCmd(opts, provisionCmdDesign, newInfraCreateAction, nil))
	cmd.AddCommand(BuildCmd(opts, deployCmdDesign, newDeployAction, nil))

	return cmd
}

func attachDebugger() {
	console := input.NewConsole(false, true, os.Stdout, input.ConsoleHandles{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}, &output.NoneFormatter{})

	console.Confirm(context.Background(), input.ConsoleOptions{
		Message:      "Ready?",
		DefaultValue: true,
	})
}

type designBuilder[F any] func(opts *internal.GlobalCommandOptions) (*cobra.Command, *F)

func createActionName(commandPath string) string {
	actionName := strings.TrimPrefix(commandPath, "azd")
	actionName = strings.TrimSpace(actionName)
	actionName = strings.ReplaceAll(actionName, " ", "-")
	return strings.ToLower(actionName)
}

func BuildCmd[F any](
	opts *internal.GlobalCommandOptions,
	buildDesign designBuilder[F],
	buildAction any,
	buildOptions *actions.BuildOptions) *cobra.Command {
	cmd, flags := buildDesign(opts)
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))

	if buildOptions == nil {
		buildOptions = &actions.BuildOptions{}
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ctx = tools.WithInstalledCheckCache(ctx)

		registerCommonDependencies(container.Global)
		registerInstance(container.Global, ctx)
		registerInstance(container.Global, flags)
		registerInstance(container.Global, buildOptions)
		registerInstance(container.Global, opts)
		registerInstance(container.Global, cmd)
		registerInstance(container.Global, args)

		actionName := createActionName(cmd.CommandPath())
		container.NamedSingletonLazy(actionName, buildAction)

		var console input.Console
		err := container.Resolve(&console)
		if err != nil {
			return fmt.Errorf("failed resolving console : %w", err)
		}

		var action actions.Action
		err = container.NamedResolve(&action, actionName)
		if err != nil {
			return fmt.Errorf("failed resolving action '%s' : %w", actionName, err)
		}

		middleware.SetContainer(container.Global)
		middleware.Use("debug", middleware.NewDebugMiddleware)
		middleware.Use("telemetry", middleware.NewTelemetryMiddleware)

		runOptions := middleware.Options{
			Name:    cmd.CommandPath(),
			Aliases: cmd.Aliases,
		}

		actionResult, err := middleware.RunAction(ctx, runOptions, action)
		// At this point, we know that there might be an error, so we can silence cobra from showing it after us.
		cmd.SilenceErrors = true

		// It is valid for a command to return a nil action result and error. If we have a result or an error, display it,
		// otherwise don't print anything.
		if actionResult != nil || err != nil {
			console.MessageUxItem(ctx, actions.ToUxItem(actionResult, err))
		}

		return err
	}

	return cmd
}
