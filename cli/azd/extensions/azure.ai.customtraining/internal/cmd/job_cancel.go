// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azure.ai.customtraining/internal/utils"
	"azure.ai.customtraining/pkg/client"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newJobCancelCommand() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel a running training job",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if name == "" {
				return fmt.Errorf("--name is required: provide the job name/ID to cancel")
			}

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

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

			fmt.Printf("Cancelling job '%s'...\n", name)

			if err := apiClient.CancelJob(ctx, name); err != nil {
				return fmt.Errorf("failed to cancel job: %w", err)
			}

			fmt.Printf("✓ Job '%s' cancel request submitted successfully\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Job name/ID to cancel (required)")

	return cmd
}
