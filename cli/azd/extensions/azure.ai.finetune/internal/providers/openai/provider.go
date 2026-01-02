// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package openai

import (
	"context"
	"fmt"
	"os"
	"time"

	"azure.ai.finetune/pkg/models"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/openai/openai-go/v3"
)

// OpenAIProvider implements the provider interface for OpenAI APIs
type OpenAIProvider struct {
	client *openai.Client
}

// NewOpenAIProvider creates a new OpenAI provider instance
func NewOpenAIProvider(client *openai.Client) *OpenAIProvider {
	return &OpenAIProvider{
		client: client,
	}
}

// CreateFineTuningJob creates a new fine-tuning job via OpenAI API
func (p *OpenAIProvider) CreateFineTuningJob(ctx context.Context, req *models.CreateFineTuningRequest) (*models.FineTuningJob, error) {

	params, err := ConvertInternalJobParamToOpenAiJobParams(req)
	if err != nil {
		return nil, fmt.Errorf("failed to convert internal model to openai: %w", err)
	}

	job, err := p.client.FineTuning.Jobs.New(ctx, *params)
	if err != nil {
		return nil, fmt.Errorf("failed to create fine-tuning job: %w", err)
	}

	return ConvertOpenAIJobToModel(*job), nil
}

// GetFineTuningStatus retrieves the status of a fine-tuning job
func (p *OpenAIProvider) GetFineTuningStatus(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	// TODO: Implement
	return nil, nil
}

// ListFineTuningJobs lists all fine-tuning jobs
func (p *OpenAIProvider) ListFineTuningJobs(ctx context.Context, limit int, after string) ([]*models.FineTuningJob, error) {
	jobList, err := p.client.FineTuning.Jobs.List(ctx, openai.FineTuningJobListParams{
		Limit: openai.Int(int64(limit)), // optional pagination control
		After: openai.String(after),
	})

	if err != nil {
		return nil, err
	}

	var jobs []*models.FineTuningJob

	for _, job := range jobList.Data {
		finetuningJob := ConvertOpenAIJobToModel(job)
		jobs = append(jobs, finetuningJob)
	}
	return jobs, nil
}

// GetFineTuningJobDetails retrieves detailed information about a job
func (p *OpenAIProvider) GetFineTuningJobDetails(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
	// TODO: Implement
	return nil, nil
}

// GetJobEvents retrieves events for a fine-tuning job
func (p *OpenAIProvider) GetJobEvents(ctx context.Context, jobID string, limit int, after string) ([]*models.JobEvent, error) {
	// TODO: Implement
	return nil, nil
}

// GetJobCheckpoints retrieves checkpoints for a fine-tuning job
func (p *OpenAIProvider) GetJobCheckpoints(ctx context.Context, jobID string, limit int, after string) ([]*models.JobCheckpoint, error) {
	// TODO: Implement
	return nil, nil
}

// PauseJob pauses a fine-tuning job
func (p *OpenAIProvider) PauseJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	// TODO: Implement
	return nil, nil
}

// ResumeJob resumes a paused fine-tuning job
func (p *OpenAIProvider) ResumeJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	// TODO: Implement
	return nil, nil
}

// CancelJob cancels a fine-tuning job
func (p *OpenAIProvider) CancelJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	// TODO: Implement
	return nil, nil
}

// UploadFile uploads a file for fine-tuning
func (p *OpenAIProvider) UploadFile(ctx context.Context, filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("file path cannot be empty")
	}

	// Show spinner while creating job
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "uploading the file using openai provider",
	})
	if err := spinner.Start(ctx); err != nil {
		fmt.Printf("failed to start spinner: %v\n", err)
	}

	file, err := os.Open(filePath)
	if err != nil {
		_ = spinner.Stop(ctx)
		return "", fmt.Errorf("\nfailed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	uploadedFile, err := p.client.Files.New(ctx, openai.FileNewParams{
		File:    file,
		Purpose: openai.FilePurposeFineTune,
	})

	if err != nil {
		_ = spinner.Stop(ctx)
		return "", fmt.Errorf("\nfailed to upload file: %w", err)
	}

	if uploadedFile == nil || uploadedFile.ID == "" {
		_ = spinner.Stop(ctx)
		return "", fmt.Errorf("\nuploaded file is empty")
	}

	// Poll for file processing status
	color.Yellow("\nWaiting for file to be processed")
	for {
		f, err := p.client.Files.Get(ctx, uploadedFile.ID)
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
		color.Yellow(".")
		time.Sleep(2 * time.Second)
	}

	return uploadedFile.ID, nil
}

// GetUploadedFile retrieves information about an uploaded file
func (p *OpenAIProvider) GetUploadedFile(ctx context.Context, fileID string) (interface{}, error) {
	// TODO: Implement
	return nil, nil
}

// DeployModel deploys a fine-tuned or base model
func (p *OpenAIProvider) DeployModel(ctx context.Context, req *models.DeploymentRequest) (*models.Deployment, error) {
	// TODO: Implement
	return nil, nil
}

// GetDeploymentStatus retrieves the status of a deployment
func (p *OpenAIProvider) GetDeploymentStatus(ctx context.Context, deploymentID string) (*models.Deployment, error) {
	// TODO: Implement
	return nil, nil
}

// ListDeployments lists all deployments
func (p *OpenAIProvider) ListDeployments(ctx context.Context, limit int, after string) ([]*models.Deployment, error) {
	// TODO: Implement
	return nil, nil
}

// UpdateDeployment updates deployment configuration
func (p *OpenAIProvider) UpdateDeployment(ctx context.Context, deploymentID string, capacity int32) (*models.Deployment, error) {
	// TODO: Implement
	return nil, nil
}

// DeleteDeployment deletes a deployment
func (p *OpenAIProvider) DeleteDeployment(ctx context.Context, deploymentID string) error {
	// TODO: Implement
	return nil
}
