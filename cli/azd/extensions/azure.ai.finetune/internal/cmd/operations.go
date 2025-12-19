// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/braydonk/yaml"
	"github.com/fatih/color"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/spf13/cobra"

	JobWrapper "azure.ai.finetune/internal/tools"
)

func newOperationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Manage fine-tuning jobs",
	}

	cmd.AddCommand(newOperationSubmitCommand())
	cmd.AddCommand(newOperationShowCommand())
	cmd.AddCommand(newOperationListCommand())
	cmd.AddCommand(newOperationDeployModelCommand())

	return cmd
}

// FineTuningConfig represents the YAML configuration structure
type FineTuningConfig struct {
	Name           string                 `yaml:"name"`
	Description    string                 `yaml:"description"`
	Model          string                 `yaml:"model"`
	Method         MethodConfig           `yaml:"method"`
	ExtraBody      map[string]interface{} `yaml:"extra_body"`
	TrainingFile   string                 `yaml:"training_file"`
	ValidationFile string                 `yaml:"validation_file"`
	EnvVariables   []EnvVariable          `yaml:"environment_variables"`
}

type MethodConfig struct {
	Type          string               `yaml:"type"`
	Supervised    *SupervisedConfig    `yaml:"supervised"`
	DPO           *DPOConfig           `yaml:"dpo"`
	Reinforcement *ReinforcementConfig `yaml:"reinforcement"`
}

type DPOConfig struct {
	Hyperparameters HyperparametersConfig `yaml:"hyperparameters"`
}

type ReinforcementConfig struct {
	Hyperparameters HyperparametersConfig `yaml:"hyperparameters"`
}

type SupervisedConfig struct {
	Hyperparameters HyperparametersConfig `yaml:"hyperparameters"`
}

type HyperparametersConfig struct {
	Epochs                 int64   `yaml:"epochs"`
	BatchSize              int64   `yaml:"batch_size"`
	LearningRateMultiplier float64 `yaml:"learning_rate_multiplier"`
	PromptLossWeight       float64 `yaml:"prompt_loss_weight"`
}

type EnvVariable struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// getStatusSymbol returns a symbol representation for job status
func getStatusSymbol(status string) string {
	switch status {
	case "queued":
		return "⏳"
	case "running":
		return "🔄"
	case "succeeded":
		return "✅"
	case "failed":
		return "💥"
	case "cancelled":
		return "❌"
	default:
		return "❓"
	}
}

// formatFineTunedModel returns the model name or "NA" if blank
func formatFineTunedModel(model string) string {
	if model == "" {
		return "NA"
	}
	return model
}

func newOperationSubmitCommand() *cobra.Command {
	var filename string
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit fine tuning job",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			// Validate filename is provided
			if filename == "" {
				return fmt.Errorf("config file is required, use -f or --file flag")
			}

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			client, err := JobWrapper.GetOpenAIClientFromAzdClient(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to create openai client: %w", err)
			}

			// Read and parse the YAML file
			yamlFile, err := os.ReadFile(filename)
			if err != nil {
				return fmt.Errorf("failed to read config file: %w", err)
			}

			var config FineTuningConfig
			if err := yaml.Unmarshal(yamlFile, &config); err != nil {
				return fmt.Errorf("failed to parse config file: %w", err)
			}

			// Upload training file
			color.Green("Uploading training data...")
			trainingFileID, err := uploadFileIfLocal(ctx, client, config.TrainingFile)
			if err != nil {
				return fmt.Errorf("failed to upload training file: %w", err)
			}

			// Upload validation file if provided
			var validationFileID string
			if config.ValidationFile != "" {
				color.Green("Uploading validation data...")
				validationFileID, err = uploadFileIfLocal(ctx, client, config.ValidationFile)
				if err != nil {
					return fmt.Errorf("failed to upload validation file: %w", err)
				}
			}

			// Create fine-tuning job
			color.Green("Creating fine-tuning job...")

			jobParams := openai.FineTuningJobNewParams{
				Model:        openai.FineTuningJobNewParamsModel(config.Model),
				TrainingFile: trainingFileID,
			}

			if validationFileID != "" {
				jobParams.ValidationFile = openai.String(validationFileID)
			}

			// Set hyperparameters if provided
			if config.Method.Type == "supervised" && config.Method.Supervised != nil {

				supervisedMethod := openai.SupervisedMethodParam{
					Hyperparameters: openai.SupervisedHyperparameters{
						BatchSize: openai.SupervisedHyperparametersBatchSizeUnion{
							OfInt: openai.Int(config.Method.Supervised.Hyperparameters.BatchSize),
						},
						LearningRateMultiplier: openai.SupervisedHyperparametersLearningRateMultiplierUnion{
							OfFloat: openai.Float(config.Method.Supervised.Hyperparameters.LearningRateMultiplier),
						},
						NEpochs: openai.SupervisedHyperparametersNEpochsUnion{
							OfInt: openai.Int(config.Method.Supervised.Hyperparameters.Epochs),
						},
					},
				}

				jobParams.Method = openai.FineTuningJobNewParamsMethod{
					Type:       "supervised",
					Supervised: supervisedMethod,
				}
			} else if config.Method.Type == "dpo" && config.Method.DPO != nil {

				dpoMethod := openai.DpoMethodParam{
					Hyperparameters: openai.DpoHyperparameters{
						BatchSize: openai.DpoHyperparametersBatchSizeUnion{
							OfInt: openai.Int(config.Method.Supervised.Hyperparameters.BatchSize),
						},
						Beta: openai.DpoHyperparametersBetaUnion{
							OfAuto: constant.ValueOf[constant.Auto](),
						},
						LearningRateMultiplier: openai.DpoHyperparametersLearningRateMultiplierUnion{
							OfFloat: openai.Float(config.Method.Supervised.Hyperparameters.LearningRateMultiplier),
						},
						NEpochs: openai.DpoHyperparametersNEpochsUnion{
							OfInt: openai.Int(config.Method.Supervised.Hyperparameters.Epochs),
						},
					},
				}

				jobParams.Method = openai.FineTuningJobNewParamsMethod{
					Type: "dpo",
					Dpo:  dpoMethod,
				}
			} else if config.Method.Type == "reinforcement" && config.Method.Reinforcement != nil {

				reinforcementMethod := openai.ReinforcementMethodParam{
					Grader: openai.ReinforcementMethodGraderUnionParam{
						OfStringCheckGrader: &openai.StringCheckGraderParam{
							Input:     "input",
							Name:      "name",
							Operation: openai.StringCheckGraderOperationEq,
							Reference: "reference",
						},
					},
					Hyperparameters: openai.ReinforcementHyperparameters{
						BatchSize: openai.ReinforcementHyperparametersBatchSizeUnion{
							OfInt: openai.Int(config.Method.Supervised.Hyperparameters.BatchSize),
						},
						ComputeMultiplier: openai.ReinforcementHyperparametersComputeMultiplierUnion{
							OfAuto: constant.ValueOf[constant.Auto](),
						},
						LearningRateMultiplier: openai.ReinforcementHyperparametersLearningRateMultiplierUnion{
							OfFloat: openai.Float(config.Method.Supervised.Hyperparameters.LearningRateMultiplier),
						},
						NEpochs: openai.ReinforcementHyperparametersNEpochsUnion{
							OfInt: openai.Int(config.Method.Supervised.Hyperparameters.Epochs),
						},
						ReasoningEffort: openai.ReinforcementHyperparametersReasoningEffortDefault,
					},
				}

				jobParams.Method = openai.FineTuningJobNewParamsMethod{
					Type:          "reinforcement",
					Reinforcement: reinforcementMethod,
				}
			}

			// Submit the fine-tuning job
			job, err := client.FineTuning.Jobs.New(ctx, jobParams)
			if err != nil {
				return fmt.Errorf("failed to create fine-tuning job: %w", err)
			}

			// Print success message
			fmt.Println(strings.Repeat("=", 120))
			color.Green("\nSuccessfully submitted fine-tuning Job!\n")
			fmt.Printf("Job ID:     %s\n", job.ID)
			fmt.Printf("Model:      %s\n", job.Model)
			fmt.Printf("Status:     %s\n", job.Status)
			fmt.Printf("Created:    %d\n", job.CreatedAt)
			if job.FineTunedModel != "" {
				fmt.Printf("Fine-tuned: %s\n", job.FineTunedModel)
			}
			fmt.Println(strings.Repeat("=", 120))

			return nil
		},
	}

	cmd.Flags().StringVarP(&filename, "file", "f", "", "Path to the config file")

	return cmd
}

// uploadFileIfLocal handles local file upload or returns the file ID if it's already uploaded
func uploadFileIfLocal(ctx context.Context, client *openai.Client, filePath string) (string, error) {
	// Check if it's a local file
	if strings.HasPrefix(filePath, "local:") {
		// Remove "local:" prefix and get the actual path
		localPath := strings.TrimPrefix(filePath, "local:")
		localPath = strings.TrimSpace(localPath)

		// Resolve absolute path
		absPath, err := filepath.Abs(localPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve absolute path for %s: %w", localPath, err)
		}

		// Open the file
		data, err := os.Open(absPath)
		if err != nil {
			return "", fmt.Errorf("failed to open file %s: %w", localPath, err)
		}
		defer data.Close()

		// Upload the file
		uploadedFile, err := client.Files.New(ctx, openai.FileNewParams{
			File:    data,
			Purpose: openai.FilePurposeFineTune,
		})
		if err != nil {
			return "", fmt.Errorf("failed to upload file: %w", err)
		}

		// Wait for file processing
		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text: "Waiting for file processing...",
		})
		if err := spinner.Start(ctx); err != nil {
			fmt.Printf("Failed to start spinner: %v\n", err)
		}
		for {
			f, err := client.Files.Get(ctx, uploadedFile.ID)
			if err != nil {
				_ = spinner.Stop(ctx)
				return "", fmt.Errorf("\nfailed to check file status: %w", err)
			}

			if f.Status == openai.FileObjectStatusProcessed {
				_ = spinner.Stop(ctx)
				break
			}

			if f.Status == openai.FileObjectStatusError {
				_ = spinner.Stop(ctx)
				return "", fmt.Errorf("\nfile processing failed with status: %s", f.Status)
			}

			fmt.Print(".")
			time.Sleep(2 * time.Second)
		}
		fmt.Printf("  Uploaded: %s -> %s, status:%s\n", localPath, uploadedFile.ID, uploadedFile.Status)
		return uploadedFile.ID, nil
	}

	// If it's not a local file, assume it's already a file ID
	return filePath, nil
}

func newOperationShowCommand() *cobra.Command {
	var jobID string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the fine tuning job details",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()
			// Show spinner while fetching jobs
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: fmt.Sprintf("Fetching fine-tuning job %s...", jobID),
			})
			if err := spinner.Start(ctx); err != nil {
				fmt.Printf("Failed to start spinner: %v\n", err)
			}

			// Fetch fine-tuning job details using job wrapper
			job, err := JobWrapper.GetJobDetails(ctx, azdClient, jobID)
			_ = spinner.Stop(ctx)

			if err != nil {
				return fmt.Errorf("failed to get fine-tuning job details: %w", err)
			}

			// Print job details
			color.Green("\nFine-Tuning Job Details\n")
			fmt.Printf("Job ID:              %s\n", job.Id)
			fmt.Printf("Status:              %s %s\n", getStatusSymbol(job.Status), job.Status)
			fmt.Printf("Model:               %s\n", job.Model)
			fmt.Printf("Fine-tuned Model:    %s\n", formatFineTunedModel(job.FineTunedModel))
			fmt.Printf("Created At:          %s\n", job.CreatedAt)
			if job.FinishedAt != "" {
				fmt.Printf("Finished At:         %s\n", job.FinishedAt)
			}
			fmt.Printf("Method:              %s\n", job.Method)
			fmt.Printf("Training File:       %s\n", job.TrainingFile)
			if job.ValidationFile != "" {
				fmt.Printf("Validation File:     %s\n", job.ValidationFile)
			}

			// Print hyperparameters if available
			if job.Hyperparameters != nil {
				fmt.Println("\nHyperparameters:")
				fmt.Printf("  Batch Size:              %d\n", job.Hyperparameters.BatchSize)
				fmt.Printf("  Learning Rate Multiplier: %f\n", job.Hyperparameters.LearningRateMultiplier)
				fmt.Printf("  N Epochs:                %d\n", job.Hyperparameters.NEpochs)
			}

			// Fetch and print events
			eventsSpinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "Fetching job events...",
			})
			if err := eventsSpinner.Start(ctx); err != nil {
				fmt.Printf("Failed to start spinner: %v\n", err)
			}

			events, err := JobWrapper.GetJobEvents(ctx, azdClient, jobID)
			_ = eventsSpinner.Stop(ctx)

			if err != nil {
				fmt.Printf("Warning: failed to fetch job events: %v\n", err)
			} else if events != nil && len(events.Data) > 0 {
				fmt.Println("\nJob Events:")
				for i, event := range events.Data {
					fmt.Printf("  %d. [%s] %s - %s\n", i+1, event.Level, event.CreatedAt, event.Message)
				}
				if events.HasMore {
					fmt.Println("  ... (more events available)")
				}
			}

			// Fetch and print checkpoints if job is completed
			if job.Status == "succeeded" {
				checkpointsSpinner := ux.NewSpinner(&ux.SpinnerOptions{
					Text: "Fetching job checkpoints...",
				})
				if err := checkpointsSpinner.Start(ctx); err != nil {
					fmt.Printf("Failed to start spinner: %v\n", err)
				}

				checkpoints, err := JobWrapper.GetJobCheckPoints(ctx, azdClient, jobID)
				_ = checkpointsSpinner.Stop(ctx)

				if err != nil {
					fmt.Printf("Warning: failed to fetch job checkpoints: %v\n", err)
				} else if checkpoints != nil && len(checkpoints.Data) > 0 {
					fmt.Println("\nJob Checkpoints:")
					for i, checkpoint := range checkpoints.Data {
						fmt.Printf("  %d. Checkpoint ID: %s\n", i+1, checkpoint.ID)
						fmt.Printf("     Checkpoint Name:       %s\n", checkpoint.FineTunedModelCheckpoint)
						fmt.Printf("     Created On:            %s\n", checkpoint.CreatedAt)
						fmt.Printf("     Step Number:           %d\n", checkpoint.StepNumber)
						if checkpoint.Metrics != nil {
							fmt.Printf("     Full Validation Loss:  %.6f\n", checkpoint.Metrics.FullValidLoss)
						}
					}
					if checkpoints.HasMore {
						fmt.Println("  ... (more checkpoints available)")
					}
				}
			}

			fmt.Println(strings.Repeat("=", 120))

			return nil
		},
	}
	cmd.Flags().StringVarP(&jobID, "job-id", "i", "", "Fine-tuning job ID")
	cmd.MarkFlagRequired("job-id")
	return cmd
}

func newOperationListCommand() *cobra.Command {
	var top int
	var after string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the fine tuning jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Show spinner while fetching jobs
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "Fetching fine-tuning jobs...",
			})
			if err := spinner.Start(ctx); err != nil {
				fmt.Printf("Failed to start spinner: %v\n", err)
			}

			// List fine-tuning jobs using job wrapper
			jobs, err := JobWrapper.ListJobs(ctx, azdClient, top, after)
			_ = spinner.Stop(ctx)

			if err != nil {
				return fmt.Errorf("failed to list fine-tuning jobs: %w", err)
			}

			for i, job := range jobs {
				fmt.Printf("\n%d. Job ID: %s | Status: %s %s | Model: %s | Fine-tuned: %s | Created: %s",
					i+1, job.Id, getStatusSymbol(job.Status), job.Status, job.Model, formatFineTunedModel(job.FineTunedModel), job.CreatedAt)
			}

			fmt.Printf("\nTotal jobs: %d\n", len(jobs))

			return nil
		},
	}
	cmd.Flags().IntVarP(&top, "top", "t", 50, "Number of fine-tuning jobs to list")
	cmd.Flags().StringVarP(&after, "after", "a", "", "Cursor for pagination")
	return cmd
}

func newOperationPauseJobCommand() *cobra.Command {
	var jobID string

	cmd := &cobra.Command{
		Use:   "Pause",
		Short: "Pause fine tuning job",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			fmt.Println(strings.Repeat("-", 120))

			// List fine-tuning jobs
			client, err := JobWrapper.GetOpenAIClientFromAzdClient(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to create openai client: %w", err)
			}
			client.FineTuning.Jobs.Pause(ctx, jobID)

			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "job-id", "i", "", "Fine-tuning job ID")

	return cmd
}

func newOperationDeployModelCommand() *cobra.Command {
	var jobID string
	var deploymentName string
	var modelFormat string
	var sku string
	var version string
	var capacity int32

	cmd := &cobra.Command{
		Use:   "Deploy",
		Short: "Pause fine tuned",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()
			// Create Azure credential - TODO
			envValueMap := make(map[string]string)

			if envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
				env := envResponse.Environment
				envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
					Name: env.Name,
				})
				if err != nil {
					return fmt.Errorf("failed to get environment values: %w", err)
				}

				for _, value := range envValues.KeyValues {
					envValueMap[value.Key] = value.Value
				}
			}

			azureContext := &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					TenantId:       envValueMap["AZURE_TENANT_ID"],
					SubscriptionId: envValueMap["AZURE_SUBSCRIPTION_ID"],
					Location:       envValueMap["AZURE_LOCATION"],
				},
				Resources: []string{},
			}
			openAiClient, err := JobWrapper.GetOpenAIClientFromAzdClient(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to create openai client: %w", err)
			}
			fineTunedModel, err := openAiClient.FineTuning.Jobs.Get(ctx, jobID)
			credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
				TenantID:                   azureContext.Scope.TenantId,
				AdditionallyAllowedTenants: []string{"*"},
			})
			if err != nil {
				return fmt.Errorf("failed to create azure credential: %w", err)
			}

			// Get Azure credentials and endpoint - TODO
			// You'll need to get these from your environment or config
			accountName := envValueMap["AZURE_ACCOUNT_NAME"]
			endpoint := fmt.Sprintf("https://%s.cognitiveservices.azure.com", accountName)

			if endpoint == "" {
				return fmt.Errorf("AZURE_OPENAI_ENDPOINT environment variable not set")
			}

			clientFactory, err := armcognitiveservices.NewClientFactory(envValueMap["AZURE_SUBSCRIPTION_ID"], credential, nil)
			if err != nil {
				fmt.Printf("failed to create client: %v", err)
			}
			poller, err := clientFactory.NewDeploymentsClient().BeginCreateOrUpdate(ctx,
				envValueMap["AZURE_RESOURCE_GROUP_NAME"],
				envValueMap["AZURE_ACCOUNT_NAME"],
				deploymentName,
				armcognitiveservices.Deployment{
					Properties: &armcognitiveservices.DeploymentProperties{
						Model: &armcognitiveservices.DeploymentModel{
							Name:    to.Ptr(fineTunedModel.FineTunedModel),
							Format:  to.Ptr(modelFormat),
							Version: to.Ptr(version),
						},
					},
					SKU: &armcognitiveservices.SKU{
						Name:     to.Ptr(sku),
						Capacity: to.Ptr[int32](capacity),
					},
				}, nil)
			if err != nil {
				fmt.Printf("failed to finish the request: %v", err)
			}
			res, err := poller.PollUntilDone(ctx, nil)
			if err != nil {
				fmt.Printf("failed to pull the result: %v", err)
			}
			_ = res

			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "job-id", "i", "", "Fine-tuning job ID")
	cmd.Flags().StringVarP(&deploymentName, "deployment-name", "d", "", "Deployment name")
	cmd.Flags().StringVarP(&modelFormat, "model-format", "m", "OpenAI", "Model format")
	cmd.Flags().StringVarP(&sku, "sku", "s", "Standard", "SKU for deployment")
	cmd.Flags().StringVarP(&version, "version", "v", "1", "Model version")
	cmd.Flags().Int32VarP(&capacity, "capacity", "c", 1, "Capacity for deployment")

	return cmd
}
