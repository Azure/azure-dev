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

	"azure.ai.finetune/internal/services"
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
	// cmd.AddCommand(newOperationActionCommand())
	// cmd.AddCommand(newOperationDeployModelCommand())

	return cmd
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
	var model string
	var trainingFile string
	var validationFile string
	var suffix string
	var seed int64
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "submit fine tuning job",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			if filename == "" && (model == "" || trainingFile == "") {
				return fmt.Errorf("either config file or model and training-file parameters are required")
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

			// Parse and validate the YAML configuration file if provided
			var config *models.CreateFineTuningRequest
			if filename != "" {
				color.Green("\nparsing configuration file...")
				config, err = utils.ParseCreateFineTuningRequestConfig(filename)
				if err != nil {
					_ = spinner.Stop(ctx)
					fmt.Println()
					return err
				}
			} else {
				config = &models.CreateFineTuningRequest{}
			}

			// Override config values with command-line parameters if provided
			if model != "" {
				config.BaseModel = model
			}
			if trainingFile != "" {

				config.TrainingFile = trainingFile
			}
			if validationFile != "" {
				config.ValidationFile = &validationFile
			}
			if suffix != "" {
				config.Suffix = &suffix
			}
			if seed != 0 {
				config.Seed = &seed
			}

			fineTuneSvc, err := services.NewFineTuningService(ctx, azdClient, nil)
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Println()
				return err
			}

			// Submit the fine-tuning job using CreateJob from JobWrapper
			job, err := fineTuneSvc.CreateFineTuningJob(ctx, config)
			_ = spinner.Stop(ctx)
			fmt.Println()

			if err != nil {
				return err
			}

			// Print success message
			fmt.Println("\n", strings.Repeat("=", 60))
			color.Green("\nsuccessfully submitted fine-tuning Job!\n")
			fmt.Printf("Job ID:     %s\n", job.ID)
			fmt.Printf("Model:      %s\n", job.BaseModel)
			fmt.Printf("Status:     %s\n", job.Status)
			fmt.Printf("Created:    %s\n", job.CreatedAt)
			if job.FineTunedModel != "" {
				fmt.Printf("Fine-tuned: %s\n", job.FineTunedModel)
			}
			fmt.Println(strings.Repeat("=", 60))
			return nil
		},
	}

	cmd.Flags().StringVarP(&filename, "file", "f", "", "Path to the config file.")
	cmd.Flags().StringVarP(&model, "model", "m", "", "Base model to fine-tune. Overrides config file. Required if --file is not provided")
	cmd.Flags().StringVarP(&trainingFile, "training-file", "t", "", "Training file ID or local path. Use 'local:' prefix for local paths. Required if --file is not provided")
	cmd.Flags().StringVarP(&validationFile, "validation-file", "v", "", "Validation file ID or local path. Use 'local:' prefix for local paths.")
	cmd.Flags().StringVarP(&suffix, "suffix", "s", "", "An optional string of up to 64 characters that will be added to your fine-tuned model name. Overrides config file.")
	cmd.Flags().Int64VarP(&seed, "seed", "r", 0, "Random seed for reproducibility of the job. If a seed is not specified, one will be generated for you. Overrides config file.")

	//Either config file should be provided or at least `model` & `training-file` parameters
	cmd.MarkFlagFilename("file", "yaml", "yml")
	cmd.MarkFlagsOneRequired("file", "model")
	cmd.MarkFlagsRequiredTogether("model", "training-file")
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
			fmt.Println("saa1")
			utils.PrintObject(job, utils.FormatTable)
			if job.Hyperparameters != nil {
				fmt.Println("\nConfiguration:")
				utils.PrintObjectWithIndent(job.Hyperparameters, utils.FormatTable, "  ")
			}
			fmt.Println("saa2")
			utils.PrintObject(job, utils.FormatJSON)
			fmt.Println("saa3")
			utils.PrintObject(job, utils.FormatYAML)
			fmt.Println("saa4")

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
	var output string
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
				Text: "Fine-tuning Jobs",
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
			fmt.Print("\n\n")

			if err != nil {
				return err
			}

			if output == "json" {
				utils.PrintObject(jobs, utils.FormatJSON)
			} else {
				utils.PrintObject(jobs, utils.FormatTable)
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "top", "t", 10, "Number of jobs to return")
	cmd.Flags().StringVar(&after, "after", "", "Pagination cursor")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "Output format: table, json")
	return cmd
}
