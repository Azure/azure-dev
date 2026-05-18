// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azure.ai.training/internal/utils"
	"azure.ai.training/pkg/client"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newJobDeleteCommand() *cobra.Command {
	var name string
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a training job",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if name == "" {
				return fmt.Errorf("--name is required: provide the job name/ID to delete")
			}

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Confirm destructive action. --yes skips the prompt. In non-interactive
			// mode (global --no-prompt or non-TTY) we refuse to delete without --yes
			// rather than blocking on stdin, matching azd's convention.
			if !yes {
				if rootFlags.NoPrompt {
					return fmt.Errorf(
						"refusing to delete job '%s' without confirmation; pass --yes to skip the prompt", name)
				}
				defaultNo := false
				resp, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
					Options: &azdext.ConfirmOptions{
						Message:      fmt.Sprintf("Are you sure you want to delete job '%s'?", name),
						DefaultValue: &defaultNo,
					},
				})
				if err != nil {
					return fmt.Errorf("failed to prompt for confirmation: %w", err)
				}
				if !resp.GetValue() {
					fmt.Println("Delete cancelled.")
					return nil
				}
			}

			envValues, err := utils.GetEnvironmentValues(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to get environment values: %w", err)
			}

			accountName := envValues[utils.EnvAzureAccountName]
			projectName := envValues[utils.EnvAzureProjectName]
			tenantID := envValues[utils.EnvAzureTenantID]

			if accountName == "" || projectName == "" {
				return fmt.Errorf("environment not configured. Run 'azd ai training init' first")
			}

			credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
				TenantID:                   tenantID,
				AdditionallyAllowedTenants: []string{"*"},
			})
			if err != nil {
				return fmt.Errorf("failed to create azure credential: %w", err)
			}

			endpoint := buildProjectEndpoint(accountName, projectName)
			apiClient, err := client.NewClient(endpoint, credential)
			if err != nil {
				return fmt.Errorf("failed to create API client: %w", err)
			}

			if err := apiClient.DeleteJob(ctx, name); err != nil {
				return fmt.Errorf("failed to delete job: %w", err)
			}

			fmt.Printf("✓ Job '%s' deleted.\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Job name/ID to delete (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")

	return cmd
}
