// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package providers

import (
	"context"

	"azure.ai.finetune/pkg/models"
)

// FineTuningProvider defines the interface for fine-tuning operations
// All providers (OpenAI, Azure, Anthropic, etc.) must implement this interface
type FineTuningProvider interface {
	// CreateFineTuningJob creates a new fine-tuning job
	CreateFineTuningJob(ctx context.Context, req *models.CreateFineTuningRequest) (*models.FineTuningJob, error)

	// GetFineTuningStatus retrieves the status of a fine-tuning job
	GetFineTuningStatus(ctx context.Context, jobID string) (*models.FineTuningJob, error)

	// ListFineTuningJobs lists all fine-tuning jobs
	ListFineTuningJobs(ctx context.Context, limit int, after string) ([]*models.FineTuningJob, error)

	// GetFineTuningJobDetails retrieves detailed information about a job
	GetFineTuningJobDetails(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error)

	// GetJobEvents retrieves events for a fine-tuning job
	GetJobEvents(ctx context.Context, jobID string, limit int, after string) ([]*models.JobEvent, error)

	// GetJobCheckpoints retrieves checkpoints for a fine-tuning job
	GetJobCheckpoints(ctx context.Context, jobID string, limit int, after string) ([]*models.JobCheckpoint, error)

	// PauseJob pauses a fine-tuning job
	PauseJob(ctx context.Context, jobID string) (*models.FineTuningJob, error)

	// ResumeJob resumes a paused fine-tuning job
	ResumeJob(ctx context.Context, jobID string) (*models.FineTuningJob, error)

	// CancelJob cancels a fine-tuning job
	CancelJob(ctx context.Context, jobID string) (*models.FineTuningJob, error)

	// UploadFile uploads a file for fine-tuning
	UploadFile(ctx context.Context, filePath string) (string, error)

	// GetUploadedFile retrieves information about an uploaded file
	GetUploadedFile(ctx context.Context, fileID string) (interface{}, error)
}

// ModelDeploymentProvider defines the interface for model deployment operations
// All providers must implement this interface for deployment functionality
type ModelDeploymentProvider interface {
	// DeployModel deploys a fine-tuned or base model
	DeployModel(ctx context.Context, req *models.DeploymentRequest) (*models.Deployment, error)

	// GetDeploymentStatus retrieves the status of a deployment
	GetDeploymentStatus(ctx context.Context, deploymentID string) (*models.Deployment, error)

	// ListDeployments lists all deployments
	ListDeployments(ctx context.Context, limit int, after string) ([]*models.Deployment, error)

	// UpdateDeployment updates deployment configuration
	UpdateDeployment(ctx context.Context, deploymentID string, capacity int32) (*models.Deployment, error)

	// DeleteDeployment deletes a deployment
	DeleteDeployment(ctx context.Context, deploymentID string) error
}
