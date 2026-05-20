// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"azure.ai.training/internal/azcopy"
	"azure.ai.training/internal/download"
	"azure.ai.training/internal/utils"
	"azure.ai.training/pkg/client"
	"azure.ai.training/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// terminalStatuses lists the job statuses where downloads are permitted.
var terminalStatuses = []string{"Completed", "Failed", "Canceled", "NotResponding", "Paused"}

// defaultOutputName is the sentinel value treated identically to "no output-name flag".
const defaultOutputName = "default"

func newJobDownloadCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
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
			if all && outputName != "" {
				return fmt.Errorf(
					"--all and --output-name cannot be used together. " +
						"Use --all to download every named output plus default artifacts, " +
						"or --output-name <name> to download a single output")
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
			if err := os.MkdirAll(destRoot, 0o750); err != nil {
				return fmt.Errorf("failed to create download path: %w", err)
			}

			apiClient, err := newDownloadClient(ctx)
			if err != nil {
				return err
			}
			apiClient.SetDebugBody(extCtx.Debug)

			// Step 1 — GET job, check status
			job, err := apiClient.GetJob(ctx, name)
			if err != nil {
				return fmt.Errorf("failed to get job: %w", err)
			}
			status := job.Properties.Status
			if !isTerminalStatus(status) {
				return fmt.Errorf(
					"This job is in state %s. Download is allowed only in states: %s",
					status, strings.Join(terminalStatuses, ", "),
				)
			}

			// Determine mode
			wantNamed, wantAll, wantDefault := selectDownloadMode(outputName, all)

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
				if err := os.MkdirAll(dest, 0o750); err != nil {
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
					if err := os.MkdirAll(dest, 0o750); err != nil {
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
				if err := os.MkdirAll(artifactsDest, 0o750); err != nil {
					return fmt.Errorf("failed to create artifacts dir: %w", err)
				}
				if err := downloadDefaultArtifacts(ctx, apiClient, name, artifactsDest); err != nil {
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
	if modelVer == nil {
		return fmt.Errorf("model %s/v%s: empty response from API", assetName, assetVersion)
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
	if err := azRunner.CopyContents(ctx, sasURI, dest); err != nil {
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
	if err := azRunner.CopyContents(ctx, sasURI, dest); err != nil {
		return fmt.Errorf("azcopy failed for output %q: %w", outputName, err)
	}
	return nil
}

// downloadDefaultArtifacts pulls the run history (for experimentId), then:
//  1. Pages the artifact LIST endpoint to enumerate every artifact path.
//  2. Extracts the unique root folder of each path (first segment before "/",
//     or the whole path for top-level files).
//  3. For each unique root, in parallel, pages the contentinfo endpoint with
//     that root as the ?path= prefix to collect SAS download URIs.
//  4. Downloads every collected contentUri in parallel.
//
// Rationale: the contentinfo endpoint requires a non-empty ?path= filter and
// accepts only one prefix per call, so we fan out by root folder.
func downloadDefaultArtifacts(
	ctx context.Context,
	apiClient *client.Client,
	jobName, dest string,
) error {
	var history *models.RunHistory
	if err := download.WithRetry(ctx, func() (int, error) {
		h, err := apiClient.GetRunHistory(ctx, jobName)
		if err != nil {
			return 0, err
		}
		history = h
		return 200, nil
	}); err != nil {
		return fmt.Errorf("could not retrieve run metadata for job %q, please retry: %w", jobName, err)
	}
	if history == nil {
		return fmt.Errorf("run metadata not found for job %q", jobName)
	}
	experimentID := history.ExperimentID
	if experimentID == "" {
		return fmt.Errorf("run metadata for job %q is missing experimentId; cannot list artifacts", jobName)
	}

	// 1. Enumerate every artifact path (paginated).
	fmt.Printf("  artifacts: listing for experiment %s...\n", experimentID)
	var allPaths []string
	token := ""
	for {
		var page *models.RunArtifactList
		tok := token
		if err := download.WithRetry(ctx, func() (int, error) {
			p, err := apiClient.ListRunArtifacts(ctx, jobName, experimentID, "", tok)
			if err != nil {
				return 0, err
			}
			page = p
			return 200, nil
		}); err != nil {
			return fmt.Errorf("could not list artifacts for job %q: %w", jobName, err)
		}
		if page == nil {
			break
		}
		for _, a := range page.Value {
			if a.Path != "" {
				allPaths = append(allPaths, a.Path)
			}
		}
		if page.ContinuationToken == "" {
			break
		}
		token = page.ContinuationToken
	}

	if len(allPaths) == 0 {
		fmt.Println("  artifacts: none found")
		return nil
	}

	// 2. Extract unique root segments (first segment of each path).
	rootSet := make(map[string]struct{})
	for _, p := range allPaths {
		root := p
		if i := strings.IndexAny(p, "/\\"); i >= 0 {
			root = p[:i]
		}
		if root != "" {
			rootSet[root] = struct{}{}
		}
	}
	roots := make([]string, 0, len(rootSet))
	for r := range rootSet {
		roots = append(roots, r)
	}
	sort.Strings(roots)

	// 3. Fetch contentinfo for each root in parallel; each fan-out is itself paginated.
	fmt.Printf("  artifacts: resolving SAS URIs for %d root folder(s): %v\n", len(roots), roots)

	type rootResult struct {
		root  string
		infos []*models.RunArtifactContentInfo
		err   error
	}
	rootResults := make([]rootResult, len(roots))
	sem := make(chan struct{}, download.DefaultParallelism)
	var wg sync.WaitGroup
	for i, r := range roots {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, root string) {
			defer wg.Done()
			defer func() { <-sem }()
			var collected []*models.RunArtifactContentInfo
			tok := ""
			for {
				var page *models.RunArtifactContentInfoList
				if err := download.WithRetry(ctx, func() (int, error) {
					p, err := apiClient.ListRunArtifactContentInfo(ctx, jobName, experimentID, root, tok)
					if err != nil {
						return 0, err
					}
					page = p
					return 200, nil
				}); err != nil {
					rootResults[idx] = rootResult{root: root, err: err}
					return
				}
				if page == nil {
					break
				}
				for j := range page.Value {
					collected = append(collected, &page.Value[j])
				}
				if page.ContinuationToken == "" {
					break
				}
				tok = page.ContinuationToken
			}
			rootResults[idx] = rootResult{root: root, infos: collected}
		}(i, r)
	}
	wg.Wait()

	var infos []*models.RunArtifactContentInfo
	var rootErrs []error
	for _, rr := range rootResults {
		if rr.err != nil {
			rootErrs = append(rootErrs, fmt.Errorf("root %q: %w", rr.root, rr.err))
			continue
		}
		infos = append(infos, rr.infos...)
	}
	if len(rootErrs) > 0 {
		// Aggregate every failing root so the user can see all problems at once
		// rather than only the first one in iteration order.
		return fmt.Errorf("could not list content info for %d of %d root(s): %w",
			len(rootErrs), len(roots), errors.Join(rootErrs...))
	}

	if len(infos) == 0 {
		fmt.Println("  artifacts: none found")
		return nil
	}

	// 4. Download all SAS URIs in parallel.
	fmt.Printf("  artifacts: downloading %d artifact(s) to %s...\n", len(infos), dest)
	// Sweep any leftover .tmp files from a previously interrupted run so
	// users don't see stale scratch files alongside their downloads.
	download.SweepTempFiles(dest)
	results := download.DownloadArtifacts(ctx, infos, dest, download.DefaultParallelism)

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
		len(results)-failed, len(results), utils.FormatBytes(totalBytes))
	if failed > 0 {
		return fmt.Errorf("%d artifact(s) failed to download", failed)
	}
	return nil
}

// selectDownloadMode resolves the three mutually-related download modes
// from the user-provided flags. Cobra already enforces that --all and
// --output-name are mutually exclusive, so this function only encodes the
// downstream rules:
//
//   - --output-name=<name> (anything other than "default")    → named only
//   - --all                                                   → every named output + default
//   - no flags / --output-name=default / --output-name=""     → default artifacts only
//
// Returns (wantNamed, wantAll, wantDefault).
func selectDownloadMode(outputName string, all bool) (bool, bool, bool) {
	wantNamed := outputName != "" && outputName != defaultOutputName
	wantAll := all
	wantDefault := !wantAll && !wantNamed
	return wantNamed, wantAll, wantDefault
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
	return slices.Contains(terminalStatuses, status)
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
