// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package JobWrapper

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
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
)

const (
	// OpenAI API version for Azure cognitive services
	apiVersion = "2025-04-01-preview"
	// Azure cognitive services endpoint URL pattern
	azureCognitiveServicesEndpoint = "https://%s.cognitiveservices.azure.com"
)

// JobContract represents a fine-tuning job response contract
type JobContract struct {
	Id             string                 `json:"id"`
	Status         string                 `json:"status"`
	Model          string                 `json:"model"`
	FineTunedModel string                 `json:"fine_tuned_model,omitempty"`
	CreatedAt      string                 `json:"created_at"`
	FinishedAt     *int64                 `json:"finished_at,omitempty"`
	FineTuning     map[string]interface{} `json:"fine_tuning,omitempty"`
	ResultFiles    []string               `json:"result_files,omitempty"`
	Error          *ErrorContract         `json:"error,omitempty"`
}

// ErrorContract represents an error response
type ErrorContract struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HyperparametersDetail represents hyperparameters details
type HyperparametersDetail struct {
	BatchSize              int64   `json:"batch_size,omitempty"`
	LearningRateMultiplier float64 `json:"learning_rate_multiplier,omitempty"`
	NEpochs                int64   `json:"n_epochs,omitempty"`
}

// MethodDetail represents method details
type MethodDetail struct {
	Type string `json:"type"`
}

// JobDetailContract represents a detailed fine-tuning job response contract
type JobDetailContract struct {
	Id              string                 `json:"id"`
	Status          string                 `json:"status"`
	Model           string                 `json:"model"`
	FineTunedModel  string                 `json:"fine_tuned_model,omitempty"`
	CreatedAt       string                 `json:"created_at"`
	FinishedAt      string                 `json:"finished_at,omitempty"`
	Method          string                 `json:"method,omitempty"`
	TrainingFile    string                 `json:"training_file,omitempty"`
	ValidationFile  string                 `json:"validation_file,omitempty"`
	Hyperparameters *HyperparametersDetail `json:"hyperparameters,omitempty"`
}

// EventContract represents a fine-tuning job event
type EventContract struct {
	ID        string      `json:"id"`
	CreatedAt string      `json:"created_at"`
	Level     string      `json:"level"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Type      string      `json:"type"`
}

// EventsListContract represents a list of fine-tuning job events
type EventsListContract struct {
	Data    []EventContract `json:"data"`
	HasMore bool            `json:"has_more"`
}

// CheckpointMetrics represents the metrics for a checkpoint
type CheckpointMetrics struct {
	FullValidLoss              float64 `json:"full_valid_loss,omitempty"`
	FullValidMeanTokenAccuracy float64 `json:"full_valid_mean_token_accuracy,omitempty"`
}

// CheckpointContract represents a provider-agnostic fine-tuning job checkpoint
// This allows supporting multiple AI providers (OpenAI, Azure, etc.)
type CheckpointContract struct {
	ID                       string             `json:"id"`
	CreatedAt                string             `json:"created_at"`
	FineTunedModelCheckpoint string             `json:"fine_tuned_model_checkpoint,omitempty"`
	Metrics                  *CheckpointMetrics `json:"metrics,omitempty"`
	FineTuningJobID          string             `json:"fine_tuning_job_id,omitempty"`
	StepNumber               int64              `json:"step_number,omitempty"`
}

// CheckpointsListContract represents a list of fine-tuning job checkpoints
type CheckpointsListContract struct {
	Data    []CheckpointContract `json:"data"`
	HasMore bool                 `json:"has_more"`
}

// CreateJob creates a new fine-tuning job with the provided parameters
func CreateJob(ctx context.Context, azdClient *azdext.AzdClient, params openai.FineTuningJobNewParams) (*JobContract, error) {
	client, err := GetOpenAIClientFromAzdClient(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %w", err)
	}

	// Validate required parameters
	if params.Model == "" {
		return nil, fmt.Errorf("model is required for fine-tuning job")
	}

	if params.TrainingFile == "" {
		return nil, fmt.Errorf("training_file is required for fine-tuning job")
	}

	// Show spinner while creating job
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Submitting fine-tuning job...",
	})
	if err := spinner.Start(ctx); err != nil {
		fmt.Printf("Failed to start spinner: %v\n", err)
	}

	// Create the fine-tuning job
	job, err := client.FineTuning.Jobs.New(ctx, params)
	_ = spinner.Stop(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to create fine-tuning job: %w", err)
	}

	// Convert to JobContract
	jobContract := &JobContract{
		Id:             job.ID,
		Status:         string(job.Status),
		Model:          job.Model,
		CreatedAt:      formatUnixTimestampToUTC(job.CreatedAt),
		FineTunedModel: job.FineTunedModel,
	}

	return jobContract, nil
}

// formatUnixTimestampToUTC converts Unix timestamp (seconds) to UTC time string
func formatUnixTimestampToUTC(timestamp int64) string {
	if timestamp == 0 {
		return ""
	}
	return time.Unix(timestamp, 0).UTC().Format("2006-01-02 15:04:05 UTC")
}

func GetJobDetails(ctx context.Context, azdClient *azdext.AzdClient, jobId string) (*JobDetailContract, error) {
	client, err := GetOpenAIClientFromAzdClient(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %w", err)
	}

	job, err := client.FineTuning.Jobs.Get(ctx, jobId)
	if err != nil {
		return nil, fmt.Errorf("failed to get job details: %w", err)
	}

	// Extract hyperparameters based on method type
	hyperparameters := &HyperparametersDetail{}
	hyperparameters.BatchSize = job.Hyperparameters.BatchSize.OfInt
	hyperparameters.LearningRateMultiplier = job.Hyperparameters.LearningRateMultiplier.OfFloat
	hyperparameters.NEpochs = job.Hyperparameters.NEpochs.OfInt

	// Create job detail contract
	jobDetail := &JobDetailContract{
		Id:              job.ID,
		Status:          string(job.Status),
		Model:           job.Model,
		FineTunedModel:  job.FineTunedModel,
		CreatedAt:       formatUnixTimestampToUTC(job.CreatedAt),
		FinishedAt:      formatUnixTimestampToUTC(job.FinishedAt),
		Method:          job.Method.Type,
		TrainingFile:    job.TrainingFile,
		ValidationFile:  job.ValidationFile,
		Hyperparameters: hyperparameters,
	}

	return jobDetail, nil
}

func GetJobEvents(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	jobId string,
) (*EventsListContract, error) {
	client, err := GetOpenAIClientFromAzdClient(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %w", err)
	}

	eventsList, err := client.FineTuning.Jobs.ListEvents(
		ctx,
		jobId,
		openai.FineTuningJobListEventsParams{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get job events: %w", err)
	}

	// Convert events to EventContract slice
	var events []EventContract
	for _, event := range eventsList.Data {
		eventContract := EventContract{
			ID:        event.ID,
			CreatedAt: formatUnixTimestampToUTC(event.CreatedAt),
			Level:     string(event.Level),
			Message:   event.Message,
			Data:      event.Data,
			Type:      string(event.Type),
		}
		events = append(events, eventContract)
	}

	// Return EventsListContract
	return &EventsListContract{
		Data:    events,
		HasMore: eventsList.HasMore,
	}, nil
}

func GetJobCheckPoints(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	jobId string,
) (*CheckpointsListContract, error) {
	client, err := GetOpenAIClientFromAzdClient(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %w", err)
	}

	checkpointList, err := client.FineTuning.Jobs.Checkpoints.List(
		ctx,
		jobId,
		openai.FineTuningJobCheckpointListParams{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get job checkpoints: %w", err)
	}

	// Convert checkpoints to CheckpointContract slice
	var checkpoints []CheckpointContract
	for _, checkpoint := range checkpointList.Data {
		metrics := &CheckpointMetrics{
			FullValidLoss:              checkpoint.Metrics.FullValidLoss,
			FullValidMeanTokenAccuracy: checkpoint.Metrics.FullValidMeanTokenAccuracy,
		}

		checkpointContract := CheckpointContract{
			ID:                       checkpoint.ID,
			CreatedAt:                formatUnixTimestampToUTC(checkpoint.CreatedAt),
			FineTunedModelCheckpoint: checkpoint.FineTunedModelCheckpoint,
			Metrics:                  metrics,
			FineTuningJobID:          checkpoint.FineTuningJobID,
			StepNumber:               checkpoint.StepNumber,
		}
		checkpoints = append(checkpoints, checkpointContract)
	}

	// Return CheckpointsListContract
	return &CheckpointsListContract{
		Data:    checkpoints,
		HasMore: checkpointList.HasMore,
	}, nil
}

// GetOpenAIClientFromAzdClient creates an OpenAI client from AzdClient context
func GetOpenAIClientFromAzdClient(ctx context.Context, azdClient *azdext.AzdClient) (*openai.Client, error) {
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
	endpoint := fmt.Sprintf(azureCognitiveServicesEndpoint, accountName)

	if endpoint == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_ENDPOINT environment variable not set")
	}

	// Create OpenAI client
	client := openai.NewClient(
		//azure.WithEndpoint(endpoint, apiVersion),
		option.WithBaseURL(fmt.Sprintf("%s/openai", endpoint)),
		option.WithQuery("api-version", apiVersion),
		azure.WithTokenCredential(credential),
	)
	return &client, nil
}

// UploadFileIfLocal handles local file upload or returns the file ID if it's already uploaded
func UploadFileIfLocal(ctx context.Context, azdClient *azdext.AzdClient, filePath string) (string, error) {
	client, err := GetOpenAIClientFromAzdClient(ctx, azdClient)
	if err != nil {
		return "", fmt.Errorf("failed to create OpenAI client: %w", err)
	}
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

// PauseJob pauses a fine-tuning job
func PauseJob(ctx context.Context, azdClient *azdext.AzdClient, jobId string) (*JobContract, error) {
	client, err := GetOpenAIClientFromAzdClient(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %w", err)
	}

	// Show spinner while pausing job
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: fmt.Sprintf("Pausing fine-tuning job %s...", jobId),
	})
	if err := spinner.Start(ctx); err != nil {
		fmt.Printf("Failed to start spinner: %v\n", err)
	}

	job, err := client.FineTuning.Jobs.Pause(ctx, jobId)
	_ = spinner.Stop(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to pause fine-tuning job: %w", err)
	}

	// Convert to JobContract
	jobContract := &JobContract{
		Id:             job.ID,
		Status:         string(job.Status),
		Model:          job.Model,
		CreatedAt:      formatUnixTimestampToUTC(job.CreatedAt),
		FineTunedModel: job.FineTunedModel,
	}

	return jobContract, nil
}

// ResumeJob resumes a fine-tuning job
func ResumeJob(ctx context.Context, azdClient *azdext.AzdClient, jobId string) (*JobContract, error) {
	client, err := GetOpenAIClientFromAzdClient(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %w", err)
	}

	// Show spinner while resuming job
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: fmt.Sprintf("Resuming fine-tuning job %s...", jobId),
	})
	if err := spinner.Start(ctx); err != nil {
		fmt.Printf("Failed to start spinner: %v\n", err)
	}

	job, err := client.FineTuning.Jobs.Resume(ctx, jobId)
	_ = spinner.Stop(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to resume fine-tuning job: %w", err)
	}

	// Convert to JobContract
	jobContract := &JobContract{
		Id:             job.ID,
		Status:         string(job.Status),
		Model:          job.Model,
		CreatedAt:      formatUnixTimestampToUTC(job.CreatedAt),
		FineTunedModel: job.FineTunedModel,
	}

	return jobContract, nil
}

// CancelJob cancels a fine-tuning job
func CancelJob(ctx context.Context, azdClient *azdext.AzdClient, jobId string) (*JobContract, error) {
	client, err := GetOpenAIClientFromAzdClient(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI client: %w", err)
	}

	// Show spinner while cancelling job
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: fmt.Sprintf("Cancelling fine-tuning job %s...", jobId),
	})
	if err := spinner.Start(ctx); err != nil {
		fmt.Printf("Failed to start spinner: %v\n", err)
	}

	job, err := client.FineTuning.Jobs.Cancel(ctx, jobId)
	_ = spinner.Stop(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to cancel fine-tuning job: %w", err)
	}

	// Convert to JobContract
	jobContract := &JobContract{
		Id:             job.ID,
		Status:         string(job.Status),
		Model:          job.Model,
		CreatedAt:      formatUnixTimestampToUTC(job.CreatedAt),
		FineTunedModel: job.FineTunedModel,
	}

	return jobContract, nil
}
