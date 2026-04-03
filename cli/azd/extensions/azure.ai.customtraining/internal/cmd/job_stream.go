// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"azure.ai.customtraining/internal/utils"
	"azure.ai.customtraining/pkg/client"
	"azure.ai.customtraining/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const (
	initialTailBytes    = 8192
	basePollInterval    = 1 * time.Second
	maxPollInterval     = 5 * time.Second
	discoveryRetryDelay = 5 * time.Second
	maxConsecErrors     = 3
	logPathPrefix       = "user_logs"
)

// fileState tracks per-file polling offset and display state.
type fileState struct {
	offset      int64
	headerShown bool
}

func newJobStreamCommand() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "stream",
		Short: "Stream logs from a running (or completed) training job",
		Long: "Stream log output from a training job using polling-based artifact reading.\n\n" +
			"Example:\n" +
			"  azd ai training job stream --name llama-sft",
		Args: cobra.NoArgs,
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

			fmt.Printf("Streaming logs for job '%s'...\n", name)

			// Step 1: Discover log files under user_logs.
			// Files may not exist yet if the job is still initializing, so retry.
			var logFiles []string
			var lastJobStatus string

			for {
				if err := ctx.Err(); err != nil {
					return err
				}

				artifactList, err := apiClient.ListArtifactsInPath(ctx, name, logPathPrefix)
				if err != nil {
					return fmt.Errorf("failed to discover log files: %w", err)
				}

				if artifactList != nil {
					for _, a := range artifactList.Value {
						if a.Path != "" {
							logFiles = append(logFiles, a.Path)
						}
					}
				}

				if len(logFiles) > 0 {
					break
				}

				// No log files yet — check job status via a probe call to see if it's still running.
				// Use a known-missing path; the 404 still returns X-VW-Job-Status.
				probeResp, probeErr := apiClient.GetArtifactContent(ctx, name, logPathPrefix+"/probe", nil)
				if probeErr == nil && probeResp != nil {
					probeResp.Body.Close()
					lastJobStatus = probeResp.JobStatus
				}

				if lastJobStatus != "" && models.TerminalStatuses[lastJobStatus] {
					fmt.Printf("\nJob '%s' is in terminal state '%s' with no log files.\n", name, lastJobStatus)
					fmt.Println("Use 'azd ai training job download' to download job artifacts.")
					return nil
				}

				fmt.Println("(Discovering log files...)")
				time.Sleep(discoveryRetryDelay)
			}

			sort.Strings(logFiles)

			// Step 2: Initial tail — read last 8KB from each file to show recent output.
			files := make(map[string]*fileState)
			var initialStatus string

			for _, path := range logFiles {
				tail := int64(initialTailBytes)
				resp, err := apiClient.GetArtifactContent(ctx, name, path, &client.ArtifactContentOptions{
					TailBytes: &tail,
				})
				if err != nil {
					return fmt.Errorf("failed to read initial content of %s: %w", path, err)
				}
				if resp == nil {
					// File listed but content not available yet
					files[path] = &fileState{}
					continue
				}

				initialStatus = resp.JobStatus

				// Parse total size for offset tracking
				contentLen, _ := strconv.ParseInt(resp.ContentLength, 10, 64)

				data, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					return fmt.Errorf("failed to read content of %s: %w", path, err)
				}

				files[path] = &fileState{
					offset: contentLen,
				}

				if len(data) > 0 {
					printFileHeader(path)
					files[path].headerShown = true
					fmt.Print(string(data))
					if !strings.HasSuffix(string(data), "\n") {
						fmt.Println()
					}
				}
			}

			// If job is already terminal on first read, show what we have and exit.
			if initialStatus != "" && !models.IsStreamableStatus(initialStatus) {
				fmt.Printf("\n✓ Job '%s' completed with status: %s\n", name, initialStatus)
				if models.TerminalStatuses[initialStatus] {
					fmt.Println("Use 'azd ai training job download' to download job artifacts.")
				}
				return nil
			}

			// Step 3: Polling loop
			pollInterval := basePollInterval
			consecErrors := 0

			for {
				if err := ctx.Err(); err != nil {
					return err
				}

				time.Sleep(pollInterval)

				// Re-discover files — new log files may appear during the run
				artifactList, err := apiClient.ListArtifactsInPath(ctx, name, logPathPrefix)
				if err != nil {
					consecErrors++
					if consecErrors >= maxConsecErrors {
						return fmt.Errorf("failed to list log files after %d retries: %w", maxConsecErrors, err)
					}
					pollInterval = backoff(pollInterval)
					continue
				}

				if artifactList != nil {
					for _, a := range artifactList.Value {
						if a.Path != "" {
							if _, exists := files[a.Path]; !exists {
								files[a.Path] = &fileState{}
								logFiles = append(logFiles, a.Path)
								sort.Strings(logFiles)
							}
						}
					}
				}

				// Poll each file for new content
				anyNewContent := false
				var latestStatus string

				for _, path := range logFiles {
					fs := files[path]
					offset := fs.offset

					resp, err := apiClient.GetArtifactContent(ctx, name, path, &client.ArtifactContentOptions{
						Offset: &offset,
					})
					if err != nil {
						consecErrors++
						if consecErrors >= maxConsecErrors {
							return fmt.Errorf("failed to read %s after %d retries: %w", path, maxConsecErrors, err)
						}
						continue
					}
					if resp == nil {
						// File not available yet
						continue
					}

					latestStatus = resp.JobStatus
					contentLen, _ := strconv.ParseInt(resp.ContentLength, 10, 64)

					data, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil {
						consecErrors++
						if consecErrors >= maxConsecErrors {
							return fmt.Errorf("failed to read content of %s: %w", path, err)
						}
						continue
					}

					// Reset error counter on successful read
					consecErrors = 0

					if len(data) > 0 {
						anyNewContent = true
						if !fs.headerShown {
							printFileHeader(path)
							fs.headerShown = true
						}
						fmt.Print(string(data))
						if !strings.HasSuffix(string(data), "\n") {
							fmt.Println()
						}
					}

					// Update offset to total content length
					if contentLen > fs.offset {
						fs.offset = contentLen
					}
				}

				// Adjust poll interval based on activity
				if anyNewContent {
					pollInterval = basePollInterval
				} else {
					pollInterval = backoff(pollInterval)
				}

				// Check if job has reached terminal state
				if latestStatus != "" && !models.IsStreamableStatus(latestStatus) {
					// Do one final poll to flush remaining content
					for _, path := range logFiles {
						fs := files[path]
						offset := fs.offset

						resp, err := apiClient.GetArtifactContent(ctx, name, path, &client.ArtifactContentOptions{
							Offset: &offset,
						})
						if err != nil || resp == nil {
							continue
						}

						data, err := io.ReadAll(resp.Body)
						resp.Body.Close()
						if err != nil {
							continue
						}

						contentLen, _ := strconv.ParseInt(resp.ContentLength, 10, 64)

						if len(data) > 0 {
							if !fs.headerShown {
								printFileHeader(path)
								fs.headerShown = true
							}
							fmt.Print(string(data))
							if !strings.HasSuffix(string(data), "\n") {
								fmt.Println()
							}
						}

						if contentLen > fs.offset {
							fs.offset = contentLen
						}
					}

					fmt.Printf("\n✓ Job '%s' completed with status: %s\n", name, latestStatus)
					return nil
				}
			}
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Job name/ID (required)")

	return cmd
}

func printFileHeader(path string) {
	fmt.Printf("\nStreaming %s\n", path)
	fmt.Println("==========================================")
}

// backoff doubles the interval up to maxPollInterval.
func backoff(current time.Duration) time.Duration {
	next := time.Duration(math.Min(float64(current*2), float64(maxPollInterval)))
	return next
}
