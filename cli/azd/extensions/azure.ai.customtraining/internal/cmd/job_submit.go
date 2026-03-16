// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azure.ai.customtraining/internal/service"
	"azure.ai.customtraining/internal/utils"
	"azure.ai.customtraining/pkg/client"
	"azure.ai.customtraining/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newJobSubmitCommand() *cobra.Command {
	var filePath string
	var outputFormat string

	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit a new training job from a YAML definition file",
		Long:  "Submit a new training job by providing a YAML job definition file.\n\nExample:\n  azd ai training job submit --file job.yaml",
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

			// Auto-generate job name if not provided (same pattern as AML SDK)
			if jobDef.Name == "" {
				jobDef.Name = utils.GenerateJobName()
			}

			// Resolve references (compute name → ARM ID, local paths → datastore URIs)
			resolver := service.NewJobResolver(
				service.NewDefaultComputeResolver(),
				service.NewDefaultCodeResolver(),
				service.NewDefaultInputResolver(),
			)
			if err := resolver.ResolveJobDefinition(ctx, jobDef); err != nil {
				return fmt.Errorf("failed to resolve job definition: %w", err)
			}

			// Build REST payload from YAML definition
			jobResource := buildJobResource(jobDef)

			fmt.Printf("Submitting command job: %s\n\n", jobDef.Name)

<<<<<<< HEAD:cli/azd/extensions/azure.ai.customtraining/internal/cmd/job_submit.go
			result, err := apiClient.CreateOrUpdateJob(ctx, jobDef.Name, jobResource)
=======
				// Content-scoped naming: code-{projectName} so dedup works across jobs
				datasetName := fmt.Sprintf("code-%s", projectName)
				fmt.Printf("| Step 1: Uploading code (%s)...\n", jobDef.Code)

				result, err := uploadSvc.UploadDirectory(
					ctx, codePath, datasetName,
					fmt.Sprintf("Code for project %s", projectName),
				)
				if err != nil {
					return fmt.Errorf("failed to upload code: %w", err)
				}

				// Hash collision fallback: use job-scoped naming without dedup
				if result.Collision {
					fallbackName := fmt.Sprintf("code-%s", jobDef.Name)
					fmt.Printf("  (hash collision on %s, falling back to %s)\n", datasetName, fallbackName)
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
				for name, input := range jobDef.Inputs {
					if input.Path == "" || !service.IsLocalPath(input.Path) {
						continue
					}

					inputPath := input.Path
					if !filepath.IsAbs(inputPath) {
						inputPath = filepath.Join(yamlDir, inputPath)
					}

					// Content-scoped naming: input-{inputName} so same data reuses across jobs
					datasetName := fmt.Sprintf("input-%s", name)
					fmt.Printf("  ├─ %s: uploading %s...\n", name, input.Path)

					result, err := uploadSvc.UploadDirectory(
						ctx, inputPath, datasetName,
						fmt.Sprintf("Input %s for project %s", name, projectName),
					)
					if err != nil {
						return fmt.Errorf("failed to upload input %s: %w", name, err)
					}

					// Hash collision fallback: use job-scoped naming without dedup
					if result.Collision {
						fallbackName := fmt.Sprintf("input-%s-%s", jobDef.Name, name)
						fmt.Printf("  (hash collision on %s, falling back to %s)\n", datasetName, fallbackName)
						result, err = uploadSvc.UploadDirectoryNoDedup(
							ctx, inputPath, fallbackName, "1",
							fmt.Sprintf("Input %s for job %s", name, jobDef.Name),
						)
						if err != nil {
							return fmt.Errorf("failed to upload input %s (fallback): %w", name, err)
						}
					}

					resolvedInputs[name] = result.DatasetResourceID
					if result.Skipped {
						fmt.Printf("  ✓ %s unchanged, reusing existing upload (version: %s)\n", name, result.DatasetVersion)
					} else {
						fmt.Printf("  ✓ %s uploaded (dataset: %s, version: %s)\n", name, result.DatasetName, result.DatasetVersion)
					}
				}
				fmt.Println()
			}

			// Build REST payload with resolved IDs
			jobResource := buildJobResource(jobDef, codeID, resolvedInputs)

			fmt.Println("| Submitting job...")
			jobResult, err := apiClient.CreateOrUpdateJob(ctx, jobDef.Name, jobResource)
>>>>>>> e3ae9cd1 (Add content-based dedup for artifact uploads using SHA-256 directory hashing):cli/azd/extensions/azure.ai.customtraining/internal/cmd/job_create.go
			if err != nil {
				return fmt.Errorf("failed to create job: %w", err)
			}

			fmt.Printf("✓ Job '%s' submitted successfully\n\n", jobDef.Name)

			if err := utils.PrintObject(result, utils.OutputFormat(outputFormat)); err != nil {
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
func buildJobResource(def *utils.JobDefinition) *models.JobResource {
	job := models.CommandJob{
		JobType:              "Command",
		DisplayName:          def.DisplayName,
		Description:          def.Description,
		Command:              def.Command,
		EnvironmentID:        def.Environment,
		ComputeID:            def.Compute,
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
			} else {
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
