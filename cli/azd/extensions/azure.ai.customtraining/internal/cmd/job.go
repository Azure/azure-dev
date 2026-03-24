// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/spf13/cobra"
)

// jobsFlags holds the common flags for all job subcommands
type jobsFlags struct {
	subscriptionId  string
	projectEndpoint string
}

func newJobCommand() *cobra.Command {
	flags := &jobsFlags{}

	cmd := &cobra.Command{
		Use: "job",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return validateOrInitEnvironment(cmd.Context(), flags.subscriptionId, flags.projectEndpoint)
		},
		Short: "Manage training jobs",
	}

	cmd.PersistentFlags().StringVarP(&flags.subscriptionId, "subscription", "s", "",
		"Azure subscription ID (enables implicit init if environment not configured)")
	cmd.PersistentFlags().StringVarP(&flags.projectEndpoint, "project-endpoint", "e", "",
		"Azure AI Foundry project endpoint URL (e.g., https://account.services.ai.azure.com/api/projects/project-name)")

	cmd.AddCommand(newJobListCommand())
	cmd.AddCommand(newJobSubmitCommand())
	cmd.AddCommand(newJobShowCommand())
	cmd.AddCommand(newJobCancelCommand())

	return cmd
}
