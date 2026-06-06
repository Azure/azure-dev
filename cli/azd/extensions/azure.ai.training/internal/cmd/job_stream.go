// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azure.ai.training/internal/service"
	"azure.ai.training/internal/utils"
	"azure.ai.training/pkg/client"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newJobStreamCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	var name string

	cmd := &cobra.Command{
		Use:   "stream",
		Short: "Stream logs from a running training job",
		Long:  "Stream live log output from a training job. Polls for new log content\nuntil the job reaches a terminal state.\n\nExample:\n  azd ai training job stream --name my-job-123",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if name == "" {
				return fmt.Errorf("--name is required")
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

			credential, err := azidentity.NewAzureDeveloperCLICredential(
				&azidentity.AzureDeveloperCLICredentialOptions{
					TenantID:                   tenantID,
					AdditionallyAllowedTenants: []string{"*"},
				},
			)
			if err != nil {
				return fmt.Errorf("failed to create azure credential: %w", err)
			}

			endpoint := buildProjectEndpoint(accountName, projectName)
			apiClient, err := client.NewClient(endpoint, credential)
			if err != nil {
				return fmt.Errorf("failed to create API client: %w", err)
			}
			apiClient.SetDebugBody(extCtx.Debug)

			streamSvc := service.NewStreamService(apiClient)
			result, err := streamSvc.StreamJobLogs(ctx, name)
			if err != nil {
				return err
			}

			fmt.Println()
			fmt.Println("Execution Summary")
			fmt.Printf("RunId: %s\n", result.JobName)
			fmt.Printf("Status: %s\n", result.Status)
			if result.StudioURL != "" {
				fmt.Printf("Web View: %s\n", result.StudioURL)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Job name/ID (required)")

	return cmd
}
