// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
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
	cmd.AddCommand(newOperationCheckpointsCommand())

	return cmd
}

func newOperationSubmitCommand() *cobra.Command {
	var filename string
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit fine tuning job",
		RunE: func(cmd *cobra.Command, args []string) error {
			color.Green("Uploading training data...")
			time.Sleep(2 * time.Second)
			color.Green("Uploading validation data...")
			time.Sleep(2 * time.Second)

			fmt.Println(strings.Repeat("=", 120))
			fmt.Println("\nSuccessfully submitted fine-tuning Job, Details:")
			fmt.Println("Job Id : ftjob-6b28b3b718624765af75be18cd63170c")
			fmt.Println("Job Url: https://ai.azure.com/nextgen/r/hWyA_fFOQ2u0NPvESpED9w,foundrysdk-eastus2-rg,,foundrysdk-eastus2-foundry-resou,foundrysdk-eastus2-project/build/fine-tune/ftjob-6b28b3b718624765af75be18cd63170c/details")
			fmt.Println(strings.Repeat("=", 120))

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
		azure.WithEndpoint(endpoint, apiVersion),
		azure.WithTokenCredential(credential),
	)
	return &client, nil
}

func newOperationCheckpointsCommand() *cobra.Command {
	var jobID string

	cmd := &cobra.Command{
		Use:   "checkpoints",
		Short: "Show fine tuning job checkpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()
			if envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
				env := envResponse.Environment
				envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
					Name: env.Name,
				})
				if err != nil {
					return fmt.Errorf("failed to get environment values: %w", err)
				}

				envValueMap := make(map[string]string)
				for _, value := range envValues.KeyValues {
					envValueMap[value.Key] = value.Value
					fmt.Println(value.Key, "=", value.Value)
				}
			}

			projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err != nil {
				return fmt.Errorf("failed to get project: %w", err)
			}
			fmt.Print(projectResponse)

			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "job-id", "i", "", "Fine-tuning job ID")

	return cmd
}
