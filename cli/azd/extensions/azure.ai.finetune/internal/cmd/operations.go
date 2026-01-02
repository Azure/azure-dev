// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"azure.ai.finetune/internal/services"
	JobWrapper "azure.ai.finetune/internal/tools"
	Utils "azure.ai.finetune/internal/utils"
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

// getStatusSymbol returns a symbol representation for job status
func getStatusSymbol(status string) string {
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
		Short: "submit fine tuning job",
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

			// Show spinner while creating job
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "creating fine-tuning job...",
			})
			if err := spinner.Start(ctx); err != nil {
				fmt.Printf("failed to start spinner: %v\n", err)
			}

			// Parse and validate the YAML configuration file
			color.Green("\nparsing configuration file...")
			config, err := Utils.ParseCreateFineTuningRequestConfig(filename)
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Println()
				return err
			}

			fineTuneSvc, err := services.NewFineTuningService(ctx, azdClient, nil)
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Println()
				return err
			}

			// Submit the fine-tuning job using CreateJob from JobWrapper
			job, err := fineTuneSvc.CreateFineTuningJob(ctx, config)
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Println()
				return err
			}

			// Print success message
			fmt.Println("\n", strings.Repeat("=", 120))
			color.Green("\nsuccessfully submitted fine-tuning Job!\n")
			fmt.Printf("Job ID:     %s\n", job.ID)
			fmt.Printf("Model:      %s\n", job.BaseModel)
			fmt.Printf("Status:     %s\n", job.Status)
			fmt.Printf("Created:    %s\n", job.CreatedAt)
			if job.FineTunedModel != "" {
				fmt.Printf("Fine-tuned: %s\n", job.FineTunedModel)
			}
			fmt.Println(strings.Repeat("=", 120))

			_ = spinner.Stop(ctx)
			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringVarP(&filename, "file", "f", "", "Path to the config file")

	return cmd
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

// newOperationListCommand creates a command to list fine-tuning jobs
func newOperationListCommand() *cobra.Command {
	var limit int
	var after string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list the fine tuning jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Show spinner while fetching jobs
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "fetching fine-tuning jobs...",
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

			for i, job := range jobs {
				fmt.Printf("\n%d. Job ID: %s | Status: %s %s | Model: %s | Fine-tuned: %s | Created: %s",
					i+1, job.ID, getStatusSymbol(string(job.Status)), job.Status, job.BaseModel, formatFineTunedModel(job.FineTunedModel), job.CreatedAt)
			}

			fmt.Printf("\ntotal jobs: %d\n", len(jobs))

			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "top", "t", 50, "number of fine-tuning jobs to list")
	cmd.Flags().StringVarP(&after, "after", "a", "", "cursor for pagination")
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
			fmt.Printf("Status:     %s %s\n", getStatusSymbol(job.Status), job.Status)
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
