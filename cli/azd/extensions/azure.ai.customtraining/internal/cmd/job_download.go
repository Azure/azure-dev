// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"azure.ai.customtraining/internal/azcopy"
	"azure.ai.customtraining/internal/download"
	"azure.ai.customtraining/internal/utils"
	"azure.ai.customtraining/pkg/client"
	"azure.ai.customtraining/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// terminalStatuses lists the job statuses where downloads are permitted.
var terminalStatuses = []string{"Completed", "Failed", "Canceled", "NotResponding", "Paused"}

// defaultOutputName is the sentinel value treated identically to "no output-name flag".
const defaultOutputName = "default"

func newJobDownloadCommand() *cobra.Command {
	var (
		name         string
		all          bool
		downloadPath string
		outputName   string
	)

	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download outputs and/or artifacts from a training job",
		Long: "Download outputs and artifacts of a training job.\n\n" +
			"Without flags, downloads the job's default artifacts (logs and run files).\n" +
			"With --output-name, downloads a single named output.\n" +
			"With --all, downloads every named output plus the default artifacts.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if name == "" {
				return fmt.Errorf("--name (-n) is required")
			}

			// Resolve destination root.
			//   --download-path / -p provided → used verbatim, exactly as the
			//     user specified (we don't append the job name; the user is in
			//     control of the directory layout).
			//   --download-path / -p omitted   → default to ./<job-name>/ so
			//     repeated downloads of different jobs don't collide in cwd.
			destRoot := downloadPath
			if destRoot == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("failed to resolve current directory: %w", err)
				}
				destRoot = filepath.Join(cwd, name)
			}
			if err := os.MkdirAll(destRoot, 0o755); err != nil {
				return fmt.Errorf("failed to create download path: %w", err)
			}

			apiClient, err := newDownloadClient(ctx)
			if err != nil {
				return err
			}
			apiClient.SetDebugBody(rootFlags.Debug)

			// Step 1 — GET job, check status
			job, err := apiClient.GetJob(ctx, name)
			if err != nil {
				return fmt.Errorf("failed to get job: %w", err)
			}
			status := job.Properties.Status
			if !isTerminalStatus(status) {
				return fmt.Errorf(
					"This job is in state %s. Download is allowed only in states %v",
					status, terminalStatuses,
				)
			}

			// Determine mode
			wantNamed := outputName != "" && outputName != defaultOutputName
			wantAll := all
			wantDefault := !wantAll && !wantNamed // covers no flags and output-name=default

			azRunner, err := azcopy.NewRunner(ctx, "")
			if err != nil {
				return fmt.Errorf("failed to initialize azcopy: %w", err)
			}

			// Single named output path
			if wantNamed {
				output, ok := job.Properties.Outputs[outputName]
				if !ok {
					return fmt.Errorf(
						"no output named %q on job %q. Available outputs: %v",
						outputName, name, listOutputNames(job.Properties.Outputs),
					)
				}
				dest := filepath.Join(destRoot, "named-outputs", outputName)
				if err := os.MkdirAll(dest, 0o755); err != nil {
					return fmt.Errorf("failed to create output dir: %w", err)
				}
				return downloadNamedOutput(ctx, apiClient, azRunner, outputName, &output, dest)
			}

			// --all path: every named output (skip "default") + default artifacts
			if wantAll {
				for outName, out := range job.Properties.Outputs {
					if outName == defaultOutputName {
						continue
					}
					out := out
					dest := filepath.Join(destRoot, "named-outputs", outName)
					if err := os.MkdirAll(dest, 0o755); err != nil {
						return fmt.Errorf("failed to create output dir: %w", err)
					}
					if err := downloadNamedOutput(ctx, apiClient, azRunner, outName, &out, dest); err != nil {
						return err
					}
				}
				// Fall through to default artifacts
			}

			// Default artifacts path (no flags, output-name=default, or --all tail)
			if wantDefault || wantAll {
				artifactsDest := filepath.Join(destRoot, "artifacts")
				if err := os.MkdirAll(artifactsDest, 0o755); err != nil {
					return fmt.Errorf("failed to create artifacts dir: %w", err)
				}
				if err := downloadDefaultArtifacts(ctx, apiClient, job, name, artifactsDest); err != nil {
					return err
				}
			}

			fmt.Printf("\n✓ Download complete: %s\n", destRoot)
			return nil
		},
	}

	cmd.Flags().StringVarP(&name, "name", "n", "", "Job name (required)")
	cmd.Flags().BoolVar(&all, "all", false, "Download all named outputs and default artifacts")
	cmd.Flags().StringVarP(&downloadPath, "download-path", "p", "",
		"Path to download files to (used as-is when provided; defaults to ./<job-name>/ in the current directory)")
	cmd.Flags().StringVar(&outputName, "output-name", "",
		"Name of the user-defined output to download. If omitted (or set to \"default\"), the default artifacts are downloaded")

	cmd.MarkFlagsMutuallyExclusive("all", "output-name")

	return cmd
}

// newDownloadClient builds an authenticated foundry client from the current azd environment.
func newDownloadClient(ctx context.Context) (*client.Client, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	envValues, err := utils.GetEnvironmentValues(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get environment values: %w", err)
	}

	accountName := envValues[utils.EnvAzureAccountName]
	projectName := envValues[utils.EnvAzureProjectName]
	tenantID := envValues[utils.EnvAzureTenantID]

	if accountName == "" || projectName == "" {
		return nil, fmt.Errorf("environment not configured. Run 'azd ai training init' first")
	}

	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenantID,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	endpoint := buildProjectEndpoint(accountName, projectName)
	apiClient, err := client.NewClient(endpoint, credential)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	return apiClient, nil
}

// downloadNamedOutput downloads a single named output (model or data asset) to dest.
func downloadNamedOutput(
	ctx context.Context,
	apiClient *client.Client,
	azRunner *azcopy.Runner,
	outputName string,
	output *models.JobOutput,
	dest string,
) error {
	if output.AssetName == "" || output.AssetVersion == "" {
		return fmt.Errorf(
			"output %q is missing assetName/assetVersion; cannot download (jobOutputType=%q)",
			outputName, output.JobOutputType,
		)
	}

	switch output.JobOutputType {
	case "safetensors_model":
		return downloadModelAsset(ctx, apiClient, azRunner, outputName, output.AssetName, output.AssetVersion, dest)
	case "uri_file", "uri_folder":
		return downloadDataAsset(ctx, apiClient, azRunner, outputName, output.AssetName, output.AssetVersion, dest)
	default:
		return fmt.Errorf(
			"output %q has unsupported jobOutputType %q (supported: safetensors_model, uri_file, uri_folder)",
			outputName, output.JobOutputType,
		)
	}
}

// downloadModelAsset resolves the SAS URI for a model and copies its container to dest via azcopy.
func downloadModelAsset(
	ctx context.Context,
	apiClient *client.Client,
	azRunner *azcopy.Runner,
	outputName, assetName, assetVersion, dest string,
) error {
	fmt.Printf("  %s: resolving model %s/v%s...\n", outputName, assetName, assetVersion)

	var modelVer *models.ModelVersion
	if err := download.WithRetry(ctx, func() (int, error) {
		mv, err := apiClient.GetModelVersion(ctx, assetName, assetVersion)
		if err != nil {
			return 0, err
		}
		modelVer = mv
		return 200, nil
	}); err != nil {
		return fmt.Errorf("failed to get model version: %w", err)
	}
	if modelVer.BlobURI == "" {
		return fmt.Errorf("model %s/v%s response missing blobUri", assetName, assetVersion)
	}

	var creds *models.CredentialsResponse
	if err := download.WithRetry(ctx, func() (int, error) {
		c, err := apiClient.GetModelCredentials(ctx, assetName, assetVersion, modelVer.BlobURI)
		if err != nil {
			return 0, err
		}
		creds = c
		return 200, nil
	}); err != nil {
		return fmt.Errorf("failed to fetch model credentials: %w", err)
	}

	sasURI := extractSasURI(creds)
	if sasURI == "" {
		return fmt.Errorf("model %s/v%s credentials response missing blobReference.credential.sasUri",
			assetName, assetVersion)
	}

	fmt.Printf("  %s: downloading model contents...\n", outputName)
	if err := azRunner.Copy(ctx, sasURI, dest); err != nil {
		return fmt.Errorf("azcopy failed for output %q: %w", outputName, err)
	}
	return nil
}

// downloadDataAsset resolves the SAS URI for a data asset and copies the container to dest.
func downloadDataAsset(
	ctx context.Context,
	apiClient *client.Client,
	azRunner *azcopy.Runner,
	outputName, assetName, assetVersion, dest string,
) error {
	fmt.Printf("  %s: resolving data asset %s/v%s...\n", outputName, assetName, assetVersion)

	var creds *models.CredentialsResponse
	if err := download.WithRetry(ctx, func() (int, error) {
		c, err := apiClient.GetDatasetCredentials(ctx, assetName, assetVersion)
		if err != nil {
			return 0, err
		}
		creds = c
		return 200, nil
	}); err != nil {
		return fmt.Errorf("failed to fetch dataset credentials: %w", err)
	}

	sasURI := extractSasURI(creds)
	if sasURI == "" {
		return fmt.Errorf("dataset %s/v%s credentials response missing blobReference.credential.sasUri",
			assetName, assetVersion)
	}

	fmt.Printf("  %s: downloading data contents...\n", outputName)
	if err := azRunner.Copy(ctx, sasURI, dest); err != nil {
		return fmt.Errorf("azcopy failed for output %q: %w", outputName, err)
	}
	return nil
}

// downloadDefaultArtifacts pulls the run history (for experimentId), pages the artifacts list,
// fetches contentinfo for each in parallel, then downloads each contentUri in parallel.
func downloadDefaultArtifacts(
	ctx context.Context,
	apiClient *client.Client,
	job *models.JobResource,
	jobName, dest string,
) error {
	trackingEndpoint, err := extractTrackingEndpoint(job)
	if err != nil {
		return err
	}

	var history *models.RunHistory
	if err := download.WithRetry(ctx, func() (int, error) {
		h, err := apiClient.GetRunHistory(ctx, jobName)
		if err != nil {
			return 0, err
		}
		history = h
		return 200, nil
	}); err != nil {
		return fmt.Errorf("failed to get run history: %w", err)
	}
	if history == nil || history.ExperimentID == "" {
		return fmt.Errorf("run history response missing experimentId for job %q", jobName)
	}
	experimentID := history.ExperimentID

	fmt.Printf("  artifacts: listing for experiment %s...\n", experimentID)
	var artifacts []models.RunArtifact
	token := ""
	for {
		var page *models.RunArtifactList
		tok := token
		if err := download.WithRetry(ctx, func() (int, error) {
			p, err := apiClient.ListRunArtifacts(ctx, trackingEndpoint, experimentID, jobName, tok)
			if err != nil {
				return 0, err
			}
			page = p
			return 200, nil
		}); err != nil {
			return fmt.Errorf("failed to list run artifacts: %w", err)
		}
		artifacts = append(artifacts, page.Value...)
		if page.ContinuationToken == "" {
			break
		}
		token = page.ContinuationToken
	}

	if len(artifacts) == 0 {
		fmt.Println("  artifacts: none found")
		return nil
	}

	fmt.Printf("  artifacts: fetching content info for %d artifact(s)...\n", len(artifacts))

	const parallelism = 8
	infos, err := fetchContentInfosParallel(ctx, apiClient, trackingEndpoint, experimentID, jobName, artifacts, parallelism)
	if err != nil {
		return err
	}

	fmt.Printf("  artifacts: downloading %d artifact(s) to %s...\n", len(infos), dest)
	results := download.DownloadArtifacts(ctx, infos, dest, parallelism)

	var failed int
	var totalBytes int64
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "    FAILED: %s — %v\n", r.Path, r.Err)
			continue
		}
		totalBytes += r.Bytes
	}
	fmt.Printf("  artifacts: %d/%d succeeded (%s)\n",
		len(results)-failed, len(results), formatBytes(totalBytes))
	if failed > 0 {
		return fmt.Errorf("%d artifact(s) failed to download", failed)
	}
	return nil
}

// fetchContentInfosParallel calls GetRunArtifactContentInfo for each artifact path concurrently.
func fetchContentInfosParallel(
	ctx context.Context,
	apiClient *client.Client,
	trackingEndpoint, experimentID, runID string,
	artifacts []models.RunArtifact,
	parallelism int,
) ([]*models.RunArtifactContentInfo, error) {
	infos := make([]*models.RunArtifactContentInfo, len(artifacts))
	errs := make([]error, len(artifacts))

	sem := make(chan struct{}, parallelism)
	done := make(chan int, len(artifacts))

	for i, a := range artifacts {
		i, a := i, a
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; done <- i }()
			err := download.WithRetry(ctx, func() (int, error) {
				info, err := apiClient.GetRunArtifactContentInfo(ctx, trackingEndpoint, experimentID, runID, a.Path)
				if err != nil {
					return 0, err
				}
				infos[i] = info
				return 200, nil
			})
			if err != nil {
				errs[i] = fmt.Errorf("contentinfo for %s: %w", a.Path, err)
			}
		}()
	}
	for range artifacts {
		<-done
	}
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return infos, nil
}

// extractTrackingEndpoint pulls properties.services.Tracking.endpoint from the job response.
func extractTrackingEndpoint(job *models.JobResource) (string, error) {
	if job == nil {
		return "", fmt.Errorf("job is nil")
	}
	tracking, ok := job.Properties.Services["Tracking"]
	if !ok {
		return "", fmt.Errorf("job missing properties.services.Tracking; cannot resolve artifacts endpoint")
	}
	tmap, ok := tracking.(map[string]any)
	if !ok {
		return "", fmt.Errorf("properties.services.Tracking has unexpected shape")
	}
	endpoint, ok := tmap["endpoint"].(string)
	if !ok || endpoint == "" {
		return "", fmt.Errorf("properties.services.Tracking.endpoint missing or not a string")
	}
	return endpoint, nil
}

// extractSasURI returns the sasUri from a credentials response, or "" if missing.
func extractSasURI(creds *models.CredentialsResponse) string {
	if creds == nil || creds.BlobReference == nil {
		return ""
	}
	return creds.BlobReference.Credential.SASUri
}

// isTerminalStatus reports whether the job state allows downloads.
func isTerminalStatus(status string) bool {
	for _, s := range terminalStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// listOutputNames returns the sorted user-defined output names (excluding "default") for error messages.
func listOutputNames(outputs map[string]models.JobOutput) []string {
	var names []string
	for k := range outputs {
		if k == defaultOutputName {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// formatBytes returns a human-readable byte size.
func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
