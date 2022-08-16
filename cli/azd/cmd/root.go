// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/spf13/cobra"
)

func NewRootCmd() *cobra.Command {
	prevDir := ""
	opts := &commands.GlobalCommandOptions{}

	cmd := &cobra.Command{
		Use:   "azd",
		Short: "Azure Developer CLI is a command-line interface for developers who build Azure solutions.",
		Long: `Azure Developer CLI is a command-line interface for developers who build Azure solutions.

To begin working with Azure Developer CLI, run the ` + withBackticks("azd up") + ` command by supplying a sample template in an empty directory:

	$ azd up –-template todo-nodejs-mongo

You can pick a template by running ` + withBackticks("azd template list") + `and then supplying the repo name as a value to ` + withBackticks("--template") + `.

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
	cmd.PersistentFlags().BoolVar(&opts.NoPrompt, "no-prompt", false, "Accepts the default value instead of prompting, or it fails if there is no default.")
	cmd.SetHelpTemplate(fmt.Sprintf("%s\nPlease let us know how we are doing: https://aka.ms/azure-dev/hats\n", cmd.HelpTemplate()))

	// the equivalent of AZURE_CORE_COLLECT_TELEMETRY
	opts.EnableTelemetry = os.Getenv("AZURE_DEV_COLLECT_TELEMETRY") != "no"

	cmd.AddCommand(deployCmd(opts))
	cmd.AddCommand(downCmd(opts))
	cmd.AddCommand(envCmd(opts))
	cmd.AddCommand(infraCmd(opts))
	cmd.AddCommand(initCmd(opts))
	cmd.AddCommand(loginCmd(opts))
	cmd.AddCommand(monitorCmd(opts))
	cmd.AddCommand(pipelineCmd(opts))
	cmd.AddCommand(provisionCmd(opts))
	cmd.AddCommand(restoreCmd(opts))
	cmd.AddCommand(upCmd(opts))
	cmd.AddCommand(templatesCmd(opts))
	cmd.AddCommand(versionCmd(opts))
	cmd.AddCommand(telemetryCmd(opts))

	return cmd
}

func Execute(args []string) error {
	tempRootCmd := NewRootCmd()
	tempRootCmd.SetArgs(args)
	return tempRootCmd.Execute()
}
