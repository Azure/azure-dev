// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"

	FTYaml "azure.ai.finetune/internal/fine_tuning_yaml"
	"azure.ai.finetune/internal/services"
	JobWrapper "azure.ai.finetune/internal/tools"
	"azure.ai.finetune/internal/utils"
	"azure.ai.finetune/pkg/models"
)

func newOperationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "jobs",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return validateEnvironment(cmd.Context())
		},
		Short: "Manage fine-tuning jobs",
	}

	cmd.AddCommand(newOperationSubmitCommand())
	cmd.AddCommand(newOperationShowCommand())
	cmd.AddCommand(newOperationListCommand())
	cmd.AddCommand(newOperationActionCommand())
	cmd.AddCommand(newOperationDeployModelCommand())

	return cmd
}

// getStatusSymbolFromString returns a symbol representation for job status
func getStatusSymbolFromString(status string) string {
	switch status {
	case "pending":
		return "⌛"
	case "queued":
		return "📚"
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

			// Parse and validate the YAML configuration file
			color.Green("Parsing configuration file...")
			config, err := FTYaml.ParseFineTuningConfig(filename)
			if err != nil {
				return err
			}

			// Upload training file

			trainingFileID, err := JobWrapper.UploadFileIfLocal(ctx, azdClient, config.TrainingFile)
			if err != nil {
				return fmt.Errorf("failed to upload training file: %w", err)
			}

			// Upload validation file if provided
			var validationFileID string
			if config.ValidationFile != "" {
				validationFileID, err = JobWrapper.UploadFileIfLocal(ctx, azdClient, config.ValidationFile)
				if err != nil {
					return fmt.Errorf("failed to upload validation file: %w", err)
				}
			}

			// Create fine-tuning job
			// Convert YAML configuration to OpenAI job parameters
			jobParams, err := ConvertYAMLToJobParams(config, trainingFileID, validationFileID)
			if err != nil {
				return fmt.Errorf("failed to convert configuration to job parameters: %w", err)
			}

			// Submit the fine-tuning job using CreateJob from JobWrapper
			job, err := JobWrapper.CreateJob(ctx, azdClient, jobParams)
			if err != nil {
				return err
			}

			// Print success message
			fmt.Println(strings.Repeat("=", 120))
			color.Green("\nSuccessfully submitted fine-tuning Job!\n")
			fmt.Printf("Job ID:     %s\n", job.Id)
			fmt.Printf("Model:      %s\n", job.Model)
			fmt.Printf("Status:     %s\n", job.Status)
			fmt.Printf("Created:    %s\n", job.CreatedAt)
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

// newOperationShowCommand creates a command to show the fine-tuning job details
func newOperationShowCommand() *cobra.Command {
	var jobID string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show fine-tuning job details.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Show spinner while fetching job
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: fmt.Sprintf("Fetching fine-tuning job %s...", jobID),
			})
			if err := spinner.Start(ctx); err != nil {
				fmt.Printf("failed to start spinner: %v\n", err)
			}

			fineTuneSvc, err := services.NewFineTuningService(ctx, azdClient, nil)
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Println()
				return err
			}

			job, err := fineTuneSvc.GetFineTuningJobDetails(ctx, jobID)
			_ = spinner.Stop(ctx)
			if err != nil {
				fmt.Println()
				return err
			}

			// Display job details
			color.Green("\nFine-tuning Job Details\n")
			fmt.Printf("Job ID:              %s\n", job.ID)
			fmt.Printf("Status:              %s %s\n", utils.GetStatusSymbol(job.Status), job.Status)
			fmt.Printf("Model:               %s\n", job.Model)
			fmt.Printf("Fine-tuned Model:    %s\n", formatFineTunedModel(job.FineTunedModel))
			fmt.Printf("Created At:          %s\n", utils.FormatTime(job.CreatedAt))
			if !job.FinishedAt.IsZero() {
				fmt.Printf("Finished At:         %s\n", utils.FormatTime(job.FinishedAt))
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
				fmt.Printf("failed to start spinner: %v\n", err)
			}

			events, err := fineTuneSvc.GetJobEvents(ctx, jobID)
			_ = eventsSpinner.Stop(ctx)

			if err != nil {
				fmt.Println()
				return err
			} else if events != nil && len(events.Data) > 0 {
				fmt.Println("\nJob Events:")
				for i, event := range events.Data {
					fmt.Printf("  %d. Event ID: %s\n", i+1, event.ID)
					fmt.Printf("     [%s] %s - %s\n", event.Level, utils.FormatTime(event.CreatedAt), event.Message)
				}
				if events.HasMore {
					fmt.Println("  ... (more events available)")
				}
			}

			// Fetch and print checkpoints if job is completed
			if job.Status == models.StatusSucceeded {
				checkpointsSpinner := ux.NewSpinner(&ux.SpinnerOptions{
					Text: "Fetching job checkpoints...",
				})
				if err := checkpointsSpinner.Start(ctx); err != nil {
					fmt.Printf("failed to start spinner: %v\n", err)
				}

				checkpoints, err := fineTuneSvc.GetJobCheckpoints(ctx, jobID)
				_ = checkpointsSpinner.Stop(ctx)

				if err != nil {
					fmt.Println()
					return err
				} else if checkpoints != nil && len(checkpoints.Data) > 0 {
					fmt.Println("\nJob Checkpoints:")
					for i, checkpoint := range checkpoints.Data {
						fmt.Printf("  %d. Checkpoint ID: %s\n", i+1, checkpoint.ID)
						fmt.Printf("     Checkpoint Name:       %s\n", checkpoint.FineTunedModelCheckpoint)
						fmt.Printf("     Created On:            %s\n", utils.FormatTime(checkpoint.CreatedAt))
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

// newOperationListCommand creates a command to list fine-tuning jobs
func newOperationListCommand() *cobra.Command {
	var limit int
	var after string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List fine-tuning jobs.",
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
				fmt.Printf("failed to start spinner: %v\n", err)
			}

			fineTuneSvc, err := services.NewFineTuningService(ctx, azdClient, nil)
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Println()
				return err
			}

			jobs, err := fineTuneSvc.ListFineTuningJobs(ctx, limit, after)
			_ = spinner.Stop(ctx)
			if err != nil {
				fmt.Println()
				return err
			}

			// Display job list
			for i, job := range jobs {
				fmt.Printf("\n%d. Job ID: %s | Status: %s %s | Model: %s | Fine-tuned: %s | Created: %s",
					i+1, job.ID, utils.GetStatusSymbol(job.Status), job.Status, job.BaseModel,
					formatFineTunedModel(job.FineTunedModel), utils.FormatTime(job.CreatedAt))
			}

			fmt.Printf("\nTotal jobs: %d\n", len(jobs))

			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "top", "t", 50, "Number of fine-tuning jobs to list")
	cmd.Flags().StringVarP(&after, "after", "a", "", "Cursor for pagination")
	return cmd
}

func newOperationActionCommand() *cobra.Command {
	var jobID string
	var action string

	cmd := &cobra.Command{
		Use:   "action",
		Short: "Perform an action on a fine-tuning job (pause, resume, cancel)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Validate job ID is provided
			if jobID == "" {
				return fmt.Errorf("job-id is required")
			}

			// Validate action is provided and valid
			if action == "" {
				return fmt.Errorf("action is required (pause, resume, or cancel)")
			}

			action = strings.ToLower(action)
			if action != "pause" && action != "resume" && action != "cancel" {
				return fmt.Errorf("invalid action '%s'. Allowed values: pause, resume, cancel", action)
			}

			var job *JobWrapper.JobContract
			var err2 error

			// Execute the requested action
			switch action {
			case "pause":
				job, err2 = JobWrapper.PauseJob(ctx, azdClient, jobID)
			case "resume":
				job, err2 = JobWrapper.ResumeJob(ctx, azdClient, jobID)
			case "cancel":
				job, err2 = JobWrapper.CancelJob(ctx, azdClient, jobID)
			}

			if err2 != nil {
				return err2
			}

			// Print success message
			fmt.Println()
			fmt.Println(strings.Repeat("=", 120))
			color.Green(fmt.Sprintf("\nSuccessfully %sd fine-tuning Job!\n", action))
			fmt.Printf("Job ID:     %s\n", job.Id)
			fmt.Printf("Model:      %s\n", job.Model)
			fmt.Printf("Status:     %s %s\n", getStatusSymbolFromString(job.Status), job.Status)
			fmt.Printf("Created:    %s\n", job.CreatedAt)
			if job.FineTunedModel != "" {
				fmt.Printf("Fine-tuned: %s\n", job.FineTunedModel)
			}
			fmt.Println(strings.Repeat("=", 120))

			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "job-id", "i", "", "Fine-tuning job ID")
	cmd.Flags().StringVarP(&action, "action", "a", "", "Action to perform: pause, resume, or cancel")
	cmd.MarkFlagRequired("job-id")
	cmd.MarkFlagRequired("action")

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
		Use:   "deploy",
		Short: "Deploy a fine-tuned model to Azure Cognitive Services",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Validate required parameters
			if jobID == "" {
				return fmt.Errorf("job-id is required")
			}
			if deploymentName == "" {
				return fmt.Errorf("deployment-name is required")
			}

			// Get environment values
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

			// Create deployment configuration
			deployConfig := JobWrapper.DeploymentConfig{
				JobID:             jobID,
				DeploymentName:    deploymentName,
				ModelFormat:       modelFormat,
				SKU:               sku,
				Version:           version,
				Capacity:          capacity,
				SubscriptionID:    envValueMap["AZURE_SUBSCRIPTION_ID"],
				ResourceGroup:     envValueMap["AZURE_RESOURCE_GROUP_NAME"],
				AccountName:       envValueMap["AZURE_ACCOUNT_NAME"],
				TenantID:          envValueMap["AZURE_TENANT_ID"],
				WaitForCompletion: true,
			}

			// Deploy the model using the wrapper
			result, err := JobWrapper.DeployModel(ctx, azdClient, deployConfig)
			if err != nil {
				return err
			}

			// Print success message
			fmt.Println(strings.Repeat("=", 120))
			color.Green("\nSuccessfully deployed fine-tuned model!\n")
			fmt.Printf("Deployment Name:  %s\n", result.DeploymentName)
			fmt.Printf("Status:           %s\n", result.Status)
			fmt.Printf("Message:          %s\n", result.Message)
			fmt.Println(strings.Repeat("=", 120))

			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "job-id", "i", "", "Fine-tuning job ID")
	cmd.Flags().StringVarP(&deploymentName, "deployment-name", "d", "", "Deployment name")
	cmd.Flags().StringVarP(&modelFormat, "model-format", "m", "OpenAI", "Model format")
	cmd.Flags().StringVarP(&sku, "sku", "s", "Standard", "SKU for deployment")
	cmd.Flags().StringVarP(&version, "version", "v", "1", "Model version")
	cmd.Flags().Int32VarP(&capacity, "capacity", "c", 1, "Capacity for deployment")
	cmd.MarkFlagRequired("job-id")
	cmd.MarkFlagRequired("deployment-name")

	return cmd
}
