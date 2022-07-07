// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	prevDir := ""
	opts := &commands.GlobalCommandOptions{}

	cmd := &cobra.Command{
		Use:   "azd",
		Short: "Azure Developer CLI (azd) - A CLI for developers building Azure solutions",
		Long: `Azure Developer CLI (azd) - A CLI for developers building Azure solutions​

To begin working with azd, run the "azd up" command by supplying a sample template in an empty directory:​
		
	$ azd up –-template todo-nodejs-mongo​
		
You can pick a template by running "azd template list" and supply the repo name as value to "–-template".​
		
The most common commands from there are:​
		
	$ azd pipeline config​
	$ azd deploy
	$ azd monitor --overview
		
For more information, please visit the project page: https://aka.ms/azure-dev/devhub`,
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

			log.SetFlags(log.LstdFlags | log.Lshortfile)

			if !opts.EnableDebugLogging {
				log.SetOutput(io.Discard)
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

	cmd.CompletionOptions.HiddenDefaultCmd = true
	cmd.Flags().BoolP("help", "h", false, "Help for "+cmd.Name())
	cmd.PersistentFlags().StringVarP(&opts.EnvironmentName, "environment", "e", "", "The name of the environment to use")
	cmd.PersistentFlags().StringVarP(&opts.Cwd, "cwd", "C", "", "Set the current working directory")
	cmd.PersistentFlags().BoolVar(&opts.EnableDebugLogging, "debug", false, "Enables debug/diagnostic logging")
	cmd.PersistentFlags().BoolVar(&opts.NoPrompt, "no-prompt", false, "Accept default value instead of prompting, or fail if there is no default")
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

	return cmd
}

func Execute(args []string) error {
	tempRootCmd := newRootCmd()
	tempRootCmd.SetArgs(args)
	return tempRootCmd.Execute()
}
