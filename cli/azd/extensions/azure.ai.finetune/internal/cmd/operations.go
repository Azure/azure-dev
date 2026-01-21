// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"

	"azure.ai.finetune/internal/providers/factory"
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
	cmd.AddCommand(newOperationPauseCommand())
	cmd.AddCommand(newOperationResumeCommand())
	cmd.AddCommand(newOperationCancelCommand())
	cmd.AddCommand(newOperationDeployModelCommand())

	return cmd
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
	var logs bool
	var output string
	requiredFlag := "id"

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Shows detailed information about a specific job.",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if jobID == "" {
				return validateRequiredFlag(requiredFlag)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Show spinner while fetching job
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "Fine-Tuning Job Details",
			})
			if err := spinner.Start(ctx); err != nil {
				fmt.Printf("failed to start spinner: %v\n", err)
			}

			fineTuneSvc, err := services.NewFineTuningService(ctx, azdClient, nil)
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Print("\n\n")
				return err
			}

			job, err := fineTuneSvc.GetFineTuningJobDetails(ctx, jobID)
			_ = spinner.Stop(ctx)
			fmt.Print("\n\n")
			if err != nil {
				return err
			}

			switch output {
			case "json":
				utils.PrintObject(job, utils.FormatJSON)
			case "yaml":
				utils.PrintObject(job, utils.FormatYAML)
			case "table", "":
				views := job.ToDetailViews()
				indent := "  "
				utils.PrintObjectWithIndent(views.Details, utils.FormatTable, indent)

				fmt.Println("\nTimestamps:")
				utils.PrintObjectWithIndent(views.Timestamps, utils.FormatTable, indent)
				fmt.Println("\nConfiguration:")
				utils.PrintObjectWithIndent(views.Configuration, utils.FormatTable, indent)

				fmt.Println("\nData:")
				utils.PrintObjectWithIndent(views.Data, utils.FormatTable, indent)
			default:
				return fmt.Errorf("unsupported output format: %s (supported: table, json, yaml)", output)
			}

			if logs {
				fmt.Println()
				// Fetch and print events
				eventsSpinner := ux.NewSpinner(&ux.SpinnerOptions{
					Text: "Events:",
				})
				if err := eventsSpinner.Start(ctx); err != nil {
					fmt.Printf("failed to start spinner: %v\n", err)
				}

				events, err := fineTuneSvc.GetJobEvents(ctx, jobID)
				_ = eventsSpinner.Stop(ctx)
				fmt.Println()

				if err != nil {
					return err
				} else if events != nil && len(events.Data) > 0 {
					const eventIndent = "     "
					for _, event := range events.Data {
						fmt.Printf("%s[%s] %s\n", eventIndent, utils.FormatTime(event.CreatedAt), event.Message)
					}
					if events.HasMore {
						fmt.Println("  ... (more events available)")
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "id", "i", "", "Job ID (required)")
	cmd.Flags().BoolVar(&logs, "logs", false, "Include recent training logs")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "Output format: table, json, yaml")
	cmd.MarkFlagRequired(requiredFlag)

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
				Text: "Fine-Tuning Jobs",
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

			switch output {
			case "json":
				utils.PrintObject(jobs, utils.FormatJSON)
			case "table", "":
				utils.PrintObject(jobs, utils.FormatTable)
			default:
				return fmt.Errorf("unsupported output format: %s (supported: table, json)", output)
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "top", "t", 10, "Number of jobs to return")
	cmd.Flags().StringVar(&after, "after", "", "Pagination cursor")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "Output format: table, json")
	return cmd
}

// newOperationPauseCommand creates a command to pause a running fine-tuning job
func newOperationPauseCommand() *cobra.Command {
	var jobID string
	requiredFlag := "id"

	cmd := &cobra.Command{
		Use:   "pause",
		Short: "Pauses a running fine-tuning job.",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if jobID == "" {
				return validateRequiredFlag(requiredFlag)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Show spinner while pausing job
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "Pausing fine-tuning job...",
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

			job, err := fineTuneSvc.PauseJob(ctx, jobID)
			_ = spinner.Stop(ctx)
			fmt.Println()

			if err != nil {
				return err
			}

			// Print success message
			fmt.Println("✓ Job pause request submitted successfully")
			fmt.Println()
			fmt.Printf("  Job ID:  %s\n", job.ID)
			fmt.Printf("  Status:  %s\n", job.Status)
			fmt.Println()
			fmt.Printf("Resume with: azd ai finetune jobs resume --id %s\n", job.ID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "id", "i", "", "Job ID (required)")
	cmd.MarkFlagRequired(requiredFlag)

	return cmd
}

// newOperationResumeCommand creates a command to resume a paused fine-tuning job
func newOperationResumeCommand() *cobra.Command {
	var jobID string
	requiredFlag := "id"

	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Resumes a paused fine-tuning job.",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if jobID == "" {
				return validateRequiredFlag(requiredFlag)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Show spinner while resuming job
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "Resuming fine-tuning job...",
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

			job, err := fineTuneSvc.ResumeJob(ctx, jobID)
			_ = spinner.Stop(ctx)
			fmt.Println()

			if err != nil {
				return err
			}

			// Print success message
			fmt.Println("✓ Job resume request submitted successfully")
			fmt.Println()
			fmt.Printf("  Job ID:  %s\n", job.ID)
			fmt.Printf("  Status:  %s\n", job.Status)
			fmt.Println()
			fmt.Printf("View progress: azd ai finetune jobs show --id %s\n", job.ID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "id", "i", "", "Job ID (required)")
	cmd.MarkFlagRequired(requiredFlag)

	return cmd
}

// newOperationCancelCommand creates a command to cancel a fine-tuning job
func newOperationCancelCommand() *cobra.Command {
	var jobID string
	var force bool
	requiredFlag := "id"

	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancels a running or queued fine-tuning job.",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if jobID == "" {
				return validateRequiredFlag(requiredFlag)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Prompt for confirmation unless --force is specified
			if !force {
				fmt.Printf("Cancel fine-tuning job %s? (y/N): ", jobID)
				var response string
				fmt.Scanln(&response)
				response = strings.ToLower(strings.TrimSpace(response))
				if response != "y" && response != "yes" {
					fmt.Println("Operation aborted.")
					return nil
				}
			}

			// Show spinner while canceling job
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "Cancelling fine-tuning job...",
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

			job, err := fineTuneSvc.CancelJob(ctx, jobID)
			_ = spinner.Stop(ctx)
			fmt.Println()

			if err != nil {
				return err
			}

			// Print success message
			fmt.Println("✓ Job cancel request submitted successfully")
			fmt.Println()
			fmt.Printf("  Job ID:  %s\n", job.ID)
			fmt.Printf("  Status:  %s\n", job.Status)
			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "id", "i", "", "Job ID (required)")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	cmd.MarkFlagRequired(requiredFlag)

	return cmd
}

func newOperationDeployModelCommand() *cobra.Command {
	var jobID string
	var deploymentName string
	var modelFormat string
	var sku string
	var version string
	var capacity int32
	var noWait bool

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy a fine-tuned model to Azure Cognitive Services",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create azd client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Show spinner while deploying model
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "deploying model...",
			})
			if err := spinner.Start(ctx); err != nil {
				fmt.Printf("failed to start spinner: %v\n", err)
			}

			// Get environment values
			envValueMap := make(map[string]string)
			if envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
				env := envResponse.Environment
				envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
					Name: env.Name,
				})
				if err != nil {
					_ = spinner.Stop(ctx)
					fmt.Println()
					return fmt.Errorf("failed to get environment values: %w", err)
				}

				for _, value := range envValues.KeyValues {
					envValueMap[value.Key] = value.Value
				}
			}

			// Validate required environment variables
			requiredEnvVars := []string{
				"AZURE_SUBSCRIPTION_ID",
				"AZURE_RESOURCE_GROUP_NAME",
				"AZURE_ACCOUNT_NAME",
				"AZURE_TENANT_ID",
			}
			for _, envVar := range requiredEnvVars {
				if envValueMap[envVar] == "" {
					_ = spinner.Stop(ctx)
					fmt.Println()
					return fmt.Errorf("required environment variable %s is not set or empty", envVar)
				}
			}

			// Create deployment configuration
			deployConfig := models.DeploymentConfig{
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
				WaitForCompletion: !noWait,
			}

			// Create Azure credential
			credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
				TenantID:                   deployConfig.TenantID,
				AdditionallyAllowedTenants: []string{"*"},
			})
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Println()
				return fmt.Errorf("failed to create azure credential: %w", err)
			}

			// Initialize fine-tuning provider
			ftProvider, err := factory.NewFineTuningProvider(ctx, azdClient)
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Println()
				return fmt.Errorf("failed to create fine-tuning provider: %w", err)
			}

			// Initialize deployment service
			deployProvider, err := factory.NewModelDeploymentProvider(envValueMap["AZURE_SUBSCRIPTION_ID"], credential)
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Println()
				return fmt.Errorf("failed to create deployment provider: %w", err)
			}
			deploymentSvc := services.NewDeploymentService(deployProvider, ftProvider, nil)

			// Deploy the model
			result, err := deploymentSvc.DeployModel(ctx, &deployConfig)
			if err != nil {
				_ = spinner.Stop(ctx)
				fmt.Println()
				return fmt.Errorf("failed to deploy model: %w", err)
			}

			_ = spinner.Stop(ctx)
			fmt.Println()

			// Print success message
			fmt.Println(strings.Repeat("=", 60))
			color.Green("\nSuccessfully deployed fine-tuned model!\n")
			if result.Deployment.ID != "" {
				fmt.Printf("Deployment ID:   %s\n", result.Deployment.ID)
			}
			fmt.Printf("Deployment Name:  %s\n", result.Deployment.Name)
			fmt.Printf("Status:           %s\n", result.Status)
			fmt.Printf("Message:          %s\n", result.Message)
			fmt.Println(strings.Repeat("=", 60))

			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "job-id", "i", "", "Fine-tuning job ID (required)")
	cmd.Flags().StringVarP(&deploymentName, "deployment-name", "d", "", "Deployment name (required)")
	cmd.Flags().StringVarP(&modelFormat, "model-format", "m", "OpenAI", "Model format")
	cmd.Flags().StringVarP(&sku, "sku", "s", "GlobalStandard", "SKU for deployment")
	cmd.Flags().StringVarP(&version, "version", "v", "1", "Model version")
	cmd.Flags().Int32VarP(&capacity, "capacity", "c", 1, "Capacity units")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Do not wait for deployment to complete")
	cmd.MarkFlagRequired("job-id")
	cmd.MarkFlagRequired("deployment-name")

	return cmd
}
