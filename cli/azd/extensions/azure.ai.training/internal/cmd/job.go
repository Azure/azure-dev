// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// jobsFlags holds the common flags for all job subcommands
type jobsFlags struct {
	subscriptionId  string
	projectEndpoint string
}

func newJobCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
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
	// --project-endpoint: keep long form only; -e short collides with azd's
	// reserved -e/--environment global flag and was removed in the SDK migration.
	cmd.PersistentFlags().StringVar(&flags.projectEndpoint, "project-endpoint", "",
		"Azure AI Foundry project endpoint URL (e.g., https://account.services.ai.azure.com/api/projects/project-name)")

	cmd.AddCommand(newJobListCommand(extCtx))
	cmd.AddCommand(newJobSubmitCommand(extCtx))
	cmd.AddCommand(newJobShowCommand(extCtx))
	cmd.AddCommand(newJobDeleteCommand(extCtx))
	cmd.AddCommand(newJobCancelCommand())
	cmd.AddCommand(newJobValidateCommand())
	cmd.AddCommand(newJobStreamCommand(extCtx))
	cmd.AddCommand(newJobConnectSSHCommand(extCtx))
	cmd.AddCommand(newJobSSHProxyCommand())
	cmd.AddCommand(newJobDownloadCommand(extCtx))
	cmd.AddCommand(newJobShowServicesCommand())

	return cmd
}
