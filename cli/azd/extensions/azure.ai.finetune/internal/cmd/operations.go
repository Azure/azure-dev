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

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/braydonk/yaml"
	"github.com/fatih/color"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/spf13/cobra"
)

func newOperationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Manage fine-tuning jobs",
	}

	cmd.AddCommand(newOperationSubmitCommand())
	cmd.AddCommand(newOperationShowCommand())
	cmd.AddCommand(newOperationListCommand())
	cmd.AddCommand(newOperationPauseJobCommand())

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

			client, err := getOpenAIClientFromAzdClient(ctx, azdClient)
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
			client, err := getOpenAIClientFromAzdClient(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to create openai client: %w", err)
			}
			fmt.Print("\n")
			color.Green("\nFine-tuning Job details:%s\n", jobID)
			fmt.Println(strings.Repeat("-", 120))

			job, err := client.FineTuning.Jobs.Get(ctx, jobID)
			if err != nil {
				fmt.Printf("failed to list fine-tuning jobs: %v", err)
			}
			fmt.Printf("Job ID: %s\n", job.ID)
			fmt.Printf("Status: %s\n", job.Status)
			fmt.Printf("Model: %s\n", job.Model)
			fmt.Printf("Hyperparameters: %v\n", job.Hyperparameters)
			fmt.Printf("Fine-tuned Model: %s\n", job.FineTunedModel)
			fmt.Println()
			color.Green("List the events")
			fmt.Println(strings.Repeat("-", 120))
			page, err := client.FineTuning.Jobs.ListEvents(ctx, job.ID, openai.FineTuningJobListEventsParams{
				Limit: openai.Int(100),
			})
			if err != nil {
				panic(err)
			}
			events := make(map[string]openai.FineTuningJobEvent)
			for i := len(page.Data) - 1; i >= 0; i-- {
				event := page.Data[i]
				if _, exists := events[event.ID]; exists {
					continue
				}
				events[event.ID] = event
				timestamp := time.Unix(int64(event.CreatedAt), 0)
				fmt.Printf("- %s: %s\n", timestamp.Format(time.Kitchen), event.Message)
			}

			if job.Status == "succeeded" {
				color.Green("\nList of checkpoints!")
				fmt.Println(strings.Repeat("-", 120))
				checkpoints, err := client.FineTuning.Jobs.Checkpoints.List(ctx, job.ID, openai.FineTuningJobCheckpointListParams{
					Limit: openai.Int(100),
				})
				if err != nil {
					panic(err)
				}
				for _, checkpint := range checkpoints.Data {
					fmt.Printf("Checkpoint ID: %s\n", checkpint.ID)
				}
			}

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

			fmt.Println("\nFine-tuning Jobs: Top ", top)
			fmt.Println(strings.Repeat("-", 120))

			// List fine-tuning jobs
			client, err := getOpenAIClientFromAzdClient(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to create openai client: %w", err)
			}
			jobs, err := client.FineTuning.Jobs.List(ctx, openai.FineTuningJobListParams{
				Limit: openai.Int(int64(top)), // optional pagination control
				After: openai.String(after),
			})
			if err != nil {
				fmt.Printf("failed to list fine-tuning jobs: %v", err)
			}
			lineNum := 0
			for _, job := range jobs.Data {
				lineNum++
				fmt.Printf("Job ID: %s | Status: %s | Model: %s | Created: %d\n",
					job.ID, job.Status, job.Model, job.CreatedAt)
			}

			fmt.Printf("\nTotal jobs: %d\n", lineNum)

			return nil
		},
	}
	cmd.Flags().IntVarP(&top, "top", "t", 50, "Number of fine-tuning jobs to list")
	cmd.Flags().StringVarP(&after, "after", "a", "", "Cursor for pagination")
	return cmd
}

func getOpenAIClientFromAzdClient(ctx context.Context, azdClient *azdext.AzdClient) (*openai.Client, error) {
	// Create Azure credential - TODO
	envValueMap := make(map[string]string)

	if envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
		env := envResponse.Environment
		envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
			Name: env.Name,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get environment values: %w", err)
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

	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   azureContext.Scope.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Get Azure credentials and endpoint - TODO
	// You'll need to get these from your environment or config
	accountName := envValueMap["AZURE_ACCOUNT_NAME"]
	endpoint := fmt.Sprintf("https://%s.cognitiveservices.azure.com", accountName)

	if endpoint == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_ENDPOINT environment variable not set")
	}

	// Create OpenAI client
	apiVersion := "2025-04-01-preview"
	client := openai.NewClient(
		//azure.WithEndpoint(endpoint, apiVersion),
		option.WithBaseURL(fmt.Sprintf("%s/openai", endpoint)),
		option.WithQuery("api-version", apiVersion),
		azure.WithTokenCredential(credential),
	)
	return &client, nil
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
			client, err := getOpenAIClientFromAzdClient(ctx, azdClient)
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
