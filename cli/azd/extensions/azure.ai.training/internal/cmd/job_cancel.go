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

func newJobCancelCommand() *cobra.Command {
	var name string
	var noWait bool

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

			if noWait {
				fmt.Printf("Cancelling job '%s' (no-wait)...\n", name)
			} else {
				fmt.Printf("Cancelling job '%s'...\n", name)
			}

			result, err := apiClient.CancelJob(ctx, name, &client.CancelJobOptions{NoWait: noWait})
			if err != nil {
				return fmt.Errorf("failed to cancel job: %w", err)
			}

			switch result.Status {
			case client.CancelJobCompleted:
				fmt.Printf("✓ Job '%s' cancelled.\n", name)
			case client.CancelJobNotFound:
				fmt.Printf("Job '%s' was not found (nothing to cancel).\n", name)
			case client.CancelJobInProgress:
				if noWait {
					fmt.Printf("Cancel for job '%s' was accepted and is in progress.\n", name)
				} else {
					fmt.Printf(
						"Cancel for job '%s' is still in progress after the maximum wait; run 'azd ai training job show --name %s' to check.\n",
						name, name,
					)
				}
			case client.CancelJobAccepted:
				fmt.Printf("Cancel for job '%s' was accepted.\n", name)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Job name/ID to cancel (required)")
	cmd.Flags().BoolVar(
		&noWait, "no-wait", false,
		"Do not wait for the cancel to complete; return immediately after the server accepts the request",
	)

	return cmd
}
