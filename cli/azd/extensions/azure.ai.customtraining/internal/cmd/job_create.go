// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"

	"azure.ai.customtraining/internal/azcopy"
	"azure.ai.customtraining/internal/service"
	"azure.ai.customtraining/internal/utils"
	"azure.ai.customtraining/pkg/client"
	"azure.ai.customtraining/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func newJobCreateCommand() *cobra.Command {
	var filePath string
	var outputFormat string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new training job from a YAML definition file",
		Long:  "Create a new training job by providing a YAML job definition file.\n\nExample:\n  azd ai training job create --file job.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if filePath == "" {
				return fmt.Errorf("--file (-f) is required: provide a path to a YAML job definition file")
			}

			// Parse and validate the YAML job definition
			jobDef, err := utils.ParseJobFile(filePath)
			if err != nil {
				return err
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

			// Auto-generate job name if not provided in YAML
			if jobDef.Name == "" {
				jobDef.Name = uuid.New().String()
				fmt.Printf("Creating command job (auto-generated name): %s\n\n", jobDef.Name)
			} else {
				fmt.Printf("Creating command job: %s\n\n", jobDef.Name)
			}

			// Resolve base directory for relative paths in the YAML file
			yamlDir := filepath.Dir(filePath)

			// --- Artifact Resolution ---
			// Initialize azcopy runner (auto-detects or auto-installs)
			azRunner, err := azcopy.NewRunner(ctx, "")
			if err != nil {
				return fmt.Errorf("failed to initialize azcopy: %w", err)
			}

			uploadSvc := service.NewUploadService(apiClient, azRunner)

			// Resolve code: upload if local path
			var codeID string
			if jobDef.Code != "" && service.IsLocalPath(jobDef.Code) {
				codePath := jobDef.Code
				if !filepath.IsAbs(codePath) {
					codePath = filepath.Join(yamlDir, codePath)
				}

				// Content-scoped naming: code-{projectName}. Dedupe will be handled by version, name can be same.
				assetName := fmt.Sprintf("code-%s", projectName)
				fmt.Printf("| Step 1: Uploading code (%s)...\n", jobDef.Code)

				result, err := uploadSvc.UploadDirectory(
					ctx, codePath, assetName,
					fmt.Sprintf("Code for project %s", projectName),
				)
				if err != nil {
					return fmt.Errorf("failed to upload code: %w", err)
				}

				// Hash collision fallback: use job-scoped naming without dedup
				if result.Collision {
					fallbackName := fmt.Sprintf("code-%s", jobDef.Name)
					fmt.Printf("  (hash collision on %s, falling back to %s)\n", assetName, fallbackName)
					result, err = uploadSvc.UploadDirectoryNoDedup(
						ctx, codePath, fallbackName, "1",
						fmt.Sprintf("Code for job %s", jobDef.Name),
					)
					if err != nil {
						return fmt.Errorf("failed to upload code (fallback): %w", err)
					}
				}

				codeID = result.DatasetResourceID
				if result.Skipped {
					fmt.Printf("✓ Code unchanged, reusing existing upload (dataset: %s, version: %s)\n\n",
						result.DatasetName, result.DatasetVersion)
				} else {
					fmt.Printf("✓ Code uploaded (dataset: %s, version: %s)\n\n",
						result.DatasetName, result.DatasetVersion)
				}
			} else if jobDef.Code != "" {
				// Remote URI — pass through as-is
				codeID = jobDef.Code
			}

			// Resolve inputs: upload local paths
			resolvedInputs := make(map[string]string)
			localInputCount := 0
			for _, input := range jobDef.Inputs {
				if input.Path != "" && service.IsLocalPath(input.Path) {
					localInputCount++
				}
			}

			if localInputCount > 0 {
				fmt.Println("| Step 2: Uploading input data...")
				for inputName, input := range jobDef.Inputs {
					if input.Path == "" || !service.IsLocalPath(input.Path) {
						continue
					}

					inputPath := input.Path
					if !filepath.IsAbs(inputPath) {
						inputPath = filepath.Join(yamlDir, inputPath)
					}

					// Content-scoped naming: input-{inputName}. Dedupe will be handled by version, name can be same.
					assetName := fmt.Sprintf("input-%s", inputName)
					fmt.Printf("  ├─ %s: uploading %s...\n", inputName, input.Path)

					result, err := uploadSvc.UploadDirectory(
						ctx, inputPath, assetName,
						fmt.Sprintf("Input %s for project %s", inputName, projectName),
					)
					if err != nil {
						return fmt.Errorf("failed to upload input %s: %w", inputName, err)
					}

					// Hash collision fallback: use job-scoped naming without dedup
					if result.Collision {
						fallbackName := fmt.Sprintf("input-%s-%s", jobDef.Name, inputName)
						fmt.Printf("  (hash collision on %s, falling back to %s)\n", assetName, fallbackName)
						result, err = uploadSvc.UploadDirectoryNoDedup(
							ctx, inputPath, fallbackName, "1",
							fmt.Sprintf("Input %s for job %s", inputName, jobDef.Name),
						)
						if err != nil {
							return fmt.Errorf("failed to upload input %s (fallback): %w", inputName, err)
						}
					}

					resolvedInputs[inputName] = result.DatasetResourceID
					if result.Skipped {
						fmt.Printf("  ✓ %s unchanged, reusing existing upload (version: %s)\n", inputName, result.DatasetVersion)
					} else {
						fmt.Printf("  ✓ %s uploaded (dataset: %s, version: %s)\n", inputName, result.DatasetName, result.DatasetVersion)
					}
				}
				fmt.Println()
			}

			// Build REST payload with resolved IDs
			jobResource := buildJobResource(jobDef, codeID, resolvedInputs)

			fmt.Println("| Submitting job...")
			jobResult, err := apiClient.CreateOrUpdateJob(ctx, jobDef.Name, jobResource)
			if err != nil {
				return fmt.Errorf("failed to create job: %w", err)
			}

			fmt.Printf("✓ Job '%s' created successfully\n\n", jobDef.Name)

			if err := utils.PrintObject(jobResult, utils.OutputFormat(outputFormat)); err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&filePath, "file", "f", "", "Path to YAML job definition file (required)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "json", "Output format (table|json)")

	return cmd
}

// buildJobResource converts a parsed YAML JobDefinition into the REST API payload.
// codeID is the resolved dataset resource ID (or empty if no code).
// resolvedInputs maps input names to their resolved dataset resource IDs (for local uploads).
func buildJobResource(
	def *utils.JobDefinition, codeID string, resolvedInputs map[string]string,
) *models.JobResource {
	job := models.CommandJob{
		JobType:              "Command",
		DisplayName:          def.DisplayName,
		Description:          def.Description,
		Command:              def.Command,
		EnvironmentID:        def.Environment,
		ComputeID:            def.Compute,
		CodeID:               codeID,
		EnvironmentVariables: def.EnvironmentVariables,
	}

	if job.DisplayName == "" {
		job.DisplayName = def.Name
	}

	// Map inputs from YAML to REST model
	if len(def.Inputs) > 0 {
		job.Inputs = make(map[string]models.JobInput, len(def.Inputs))
		for name, input := range def.Inputs {
			ji := models.JobInput{
				JobInputType: input.Type,
				Mode:         input.Mode,
			}
			if input.Value != "" {
				ji.JobInputType = "literal"
				ji.Value = input.Value
			} else if resolvedID, ok := resolvedInputs[name]; ok {
				// This input was uploaded locally — use the resolved dataset resource ID
				ji.URI = resolvedID
			} else {
				// Remote URI — pass through as-is
				ji.URI = input.Path
			}
			job.Inputs[name] = ji
		}
	}

	// Map outputs
	if len(def.Outputs) > 0 {
		job.Outputs = make(map[string]models.JobOutput, len(def.Outputs))
		for name, output := range def.Outputs {
			job.Outputs[name] = models.JobOutput{
				JobOutputType: output.Type,
				Mode:          output.Mode,
				URI:           output.Path,
			}
		}
	}

	// Distribution
	if def.Distribution != "" {
		job.Distribution = &models.Distribution{
			DistributionType:        def.Distribution,
			ProcessCountPerInstance: def.ProcessPerNode,
		}
	}

	// Resources
	if def.InstanceCount > 0 {
		job.Resources = &models.ResourceConfig{
			InstanceCount: def.InstanceCount,
		}
	}

	// Limits (timeout)
	if def.Timeout != "" {
		job.Limits = &models.CommandJobLimits{
			Timeout: def.Timeout,
		}
	}

	return &models.JobResource{
		Properties: job,
		Tags:       def.Tags,
	}
}
