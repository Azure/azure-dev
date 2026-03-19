// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"azure.ai.customtraining/internal/utils"
	"azure.ai.customtraining/pkg/client"
	"azure.ai.customtraining/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newJobListCommand() *cobra.Command {
	var outputFormat string
	var skipToken string
	var tag string
	var properties string
	var includeArchived bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all training jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

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

			opts := &client.ListJobsOptions{}
			if skipToken != "" {
				opts.SkipToken = skipToken
			}
			if tag != "" {
				opts.Tag = tag
			}
			if properties != "" {
				opts.Properties = properties
			}
			opts.IncludeArchived = includeArchived

			result, err := apiClient.ListJobs(ctx, opts)
			if err != nil {
				return fmt.Errorf("failed to list jobs: %w", err)
			}

			format := utils.OutputFormat(outputFormat)

			if format == utils.FormatJSON {
				return utils.PrintObject(result.Value, format)
			}

			// Flatten to list items for table display
			items := make([]models.JobListItem, len(result.Value))
			for i, job := range result.Value {
				computeName := job.Properties.ComputeID
				// Extract just the compute name from full ARM ID for display
				if parts := strings.Split(computeName, "/"); len(parts) > 0 {
					computeName = parts[len(parts)-1]
				}

				var createdAt, createdBy string
				if job.SystemData != nil {
					createdAt = job.SystemData.CreatedAt
					createdBy = job.SystemData.CreatedBy
				}

				items[i] = models.JobListItem{
					Name:        job.Name,
					DisplayName: job.Properties.DisplayName,
					Status:      job.Properties.Status,
					JobType:     job.Properties.JobType,
					ComputeID:   computeName,
					CreatedBy:   createdBy,
					Created:     createdAt,
				}
			}

			if err := utils.PrintObject(items, utils.FormatTable); err != nil {
				return err
			}

			if nextToken := extractSkipToken(result.NextLink); nextToken != "" {
				fmt.Printf("\nMore results available. Run the following command to see the next page:\n")
				fmt.Printf("  azd ai training job list --skip-token \"%s\"\n", nextToken)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format (table|json)")
	cmd.Flags().StringVar(&skipToken, "skip-token", "", "Continuation token for next page of results")
	cmd.Flags().StringVar(&tag, "tag", "", "Filter results by tag key (e.g., --tag team)")
	cmd.Flags().StringVar(&properties, "properties", "", "Filter results by properties (comma-separated, e.g., --properties \"prop1,prop2=value2\")")
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "Include archived jobs in the results (default: active only)")

	return cmd
}

// extractSkipToken parses the $skipToken query parameter from a nextLink URL.
func extractSkipToken(nextLink string) string {
	if nextLink == "" {
		return ""
	}
	parsed, err := url.Parse(nextLink)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("$skipToken")
}
