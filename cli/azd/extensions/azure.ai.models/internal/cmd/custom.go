// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// customFlags holds the common flags for all custom model subcommands
type customFlags struct {
	subscriptionId  string
	projectEndpoint string
}

// newCustomCommand creates the "custom" command group for custom model operations.
func newCustomCommand() *cobra.Command {
	flags := &customFlags{}

	customCmd := &cobra.Command{
		Use:   "custom",
		Short: "Manage custom models in Azure AI Foundry",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if flags.projectEndpoint == "" {
				return fmt.Errorf("--project-endpoint (-e) is required.\n\n" +
					"Example: azd ai models custom list -e https://<account>.services.ai.azure.com/api/projects/<project>")
			}
			return nil
		},
	}

	customCmd.PersistentFlags().StringVarP(&flags.subscriptionId, "subscription", "s", "",
		"Azure subscription ID")
	customCmd.PersistentFlags().StringVarP(&flags.projectEndpoint, "project-endpoint", "e", "",
		"Azure AI Foundry project endpoint URL (e.g., https://account.services.ai.azure.com/api/projects/project-name)")

	customCmd.AddCommand(newCustomCreateCommand())
	customCmd.AddCommand(newCustomListCommand(flags))
	customCmd.AddCommand(newCustomShowCommand())
	customCmd.AddCommand(newCustomDeleteCommand())

	return customCmd
}
