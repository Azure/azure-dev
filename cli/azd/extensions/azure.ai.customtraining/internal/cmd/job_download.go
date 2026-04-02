// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"azure.ai.customtraining/internal/azcopy"
	"azure.ai.customtraining/internal/utils"
	"azure.ai.customtraining/pkg/client"
	"azure.ai.customtraining/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newJobDownloadCommand() *cobra.Command {
	var name string
	var downloadPath string

	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download job output artifacts to a local directory",
		Long: "Download output artifacts from a completed training job to a local directory.\n\n" +
			"Example:\n" +
			"  azd ai training job download --name llama-sft\n" +
			"  azd ai training job download --name llama-sft --path ./downloads",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if name == "" {
				return fmt.Errorf("--name is required")
			}

			// Default download path to current directory
			if downloadPath == "" {
				downloadPath = "./"
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

			// Step 1: Verify job exists and is in a terminal state
			fmt.Printf("Downloading artifacts for job '%s'...\n\n", name)

			job, err := apiClient.GetJob(ctx, name)
			if err != nil {
				return fmt.Errorf("failed to get job: %w", err)
			}

			if !models.TerminalStatuses[job.Properties.Status] {
				return fmt.Errorf(
					"job '%s' is in status '%s'. Download is only available for jobs in a terminal state "+
						"(Completed, Failed, Canceled)",
					name, job.Properties.Status,
				)
			}

			// Step 2: List all artifacts to discover output paths/prefixes
			fmt.Println("| Listing artifacts...")

			allArtifacts, err := apiClient.ListAllArtifacts(ctx, name)
			if err != nil {
				return fmt.Errorf("failed to list artifacts: %w", err)
			}

			if len(allArtifacts) == 0 {
				fmt.Println("  No artifacts found for this job.")
				return nil
			}

			// Collect unique first-level folder prefixes for batch SAS URI retrieval
			prefixes := utils.CollectArtifactPrefixes(allArtifacts)

			fmt.Printf("✓ Found %d artifacts\n\n", len(allArtifacts))

			// Step 3: Get SAS URIs for all artifacts using prefix/contentinfo (batch)
			var allSASItems []models.ArtifactContentInfo
			for _, prefix := range prefixes {
				items, err := apiClient.GetAllArtifactSASURIs(ctx, name, prefix)
				if err != nil {
					return fmt.Errorf("failed to get SAS URIs for prefix '%s': %w", prefix, err)
				}
				allSASItems = append(allSASItems, items...)
			}

			if len(allSASItems) == 0 {
				return fmt.Errorf("no downloadable SAS URIs returned for job artifacts")
			}

			// Compute total download size from SAS content info
			var totalSize int64
			for _, item := range allSASItems {
				totalSize += item.ContentLength
			}
			fmt.Printf("  Total download size: %s\n\n", utils.FormatSize(totalSize))

			// Initialize azcopy runner
			azRunner, err := azcopy.NewRunner(ctx, "")
			if err != nil {
				return fmt.Errorf("failed to initialize azcopy: %w", err)
			}

			// Resolve absolute download path
			absPath, err := filepath.Abs(downloadPath)
			if err != nil {
				return fmt.Errorf("failed to resolve download path: %w", err)
			}

			// Step 4: Download each artifact via azcopy
			fmt.Println("| Downloading...")

			for i, item := range allSASItems {
				// Preserve directory structure from artifact path
				localFilePath := filepath.Join(absPath, filepath.FromSlash(item.Path))
				localDir := filepath.Dir(localFilePath)

				if err := os.MkdirAll(localDir, 0750); err != nil {
					return fmt.Errorf("failed to create directory %s: %w", localDir, err)
				}

				// Display progress tree
				connector := "├─"
				if i == len(allSASItems)-1 {
					connector = "└─"
				}
				fmt.Printf("  %s %s (%s)\n", connector, item.Path, utils.FormatSize(item.ContentLength))

				// Download: SAS URI → local file
				if err := azRunner.Copy(ctx, item.ContentURI, localFilePath); err != nil {
					return fmt.Errorf("failed to download %s: %w", item.Path, err)
				}
			}

			fmt.Printf("\n✓ Downloaded to %s\n", absPath)

			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Job name/ID (required)")
	cmd.Flags().StringVar(&downloadPath, "path", "./",
		"Local directory to download into")

	return cmd
}
