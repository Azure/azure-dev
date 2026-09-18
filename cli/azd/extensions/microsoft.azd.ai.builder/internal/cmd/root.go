// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal/helpformat"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "builder <command> [options]",
		Short:         "Runs the Azure AI Builder.",
		Example:       "  # Start the interactive AI application builder\n  azd ai builder start",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug mode")

	rootCmd.AddCommand(newStartCommand())
	rootCmd.AddCommand(newVersionCommand())

	helpformat.Install(rootCmd, "azd ai", `Project & Environment:
  The builder uses the current azd project and environment.
  It can initialize a project and create an environment when they are missing.
  AZURE_TENANT_ID, AZURE_SUBSCRIPTION_ID, and AZURE_LOCATION are read from that
  environment's values; missing subscription and location choices are prompted for
  and saved to the environment. Use 'azd env select <name>' to switch environments.`)

	return rootCmd
}
