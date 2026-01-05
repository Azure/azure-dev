// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package services

import (
	"context"

	"azure.ai.finetune/pkg/models"
)

// FineTuningService defines the business logic interface for fine-tuning operations
type FineTuningService interface {
	// CreateFineTuningJob creates a new fine-tuning job with business validation
	CreateFineTuningJob(ctx context.Context, req *models.CreateFineTuningRequest) (*models.FineTuningJob, error)

	// GetFineTuningStatus retrieves the current status of a job
	GetFineTuningStatus(ctx context.Context, jobID string) (*models.FineTuningJob, error)

	// ListFineTuningJobs lists all fine-tuning jobs for the user
	ListFineTuningJobs(ctx context.Context, limit int, after string) ([]*models.FineTuningJob, error)

	// GetFineTuningJobDetails retrieves detailed information about a job
	GetFineTuningJobDetails(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error)

	// GetJobEvents retrieves events for a job with filtering and pagination
	GetJobEvents(ctx context.Context, jobID string) (*models.JobEventsList, error)

	// GetJobCheckpoints retrieves checkpoints for a job with pagination
	GetJobCheckpoints(ctx context.Context, jobID string) (*models.JobCheckpointsList, error)

	// PauseJob pauses a running job (if applicable)
	PauseJob(ctx context.Context, jobID string) (*models.FineTuningJob, error)

	// ResumeJob resumes a paused job (if applicable)
	ResumeJob(ctx context.Context, jobID string) (*models.FineTuningJob, error)

	// CancelJob cancels a job with proper state validation
	CancelJob(ctx context.Context, jobID string) (*models.FineTuningJob, error)

	// UploadFile uploads and validates a file
	UploadFile(ctx context.Context, filePath string) (string, error)

	// PollJobUntilCompletion polls a job until it completes or fails
	PollJobUntilCompletion(ctx context.Context, jobID string, intervalSeconds int) (*models.FineTuningJob, error)
}

// DeploymentService defines the business logic interface for model deployment operations
type DeploymentService interface {
	// DeployModel deploys a fine-tuned or base model with validation
	DeployModel(ctx context.Context, req *models.DeploymentRequest) (*models.Deployment, error)

	// GetDeploymentStatus retrieves the current status of a deployment
	GetDeploymentStatus(ctx context.Context, deploymentID string) (*models.Deployment, error)

	// ListDeployments lists all deployments for the user
	ListDeployments(ctx context.Context, limit int, after string) ([]*models.Deployment, error)

	// UpdateDeployment updates deployment configuration (e.g., capacity)
	UpdateDeployment(ctx context.Context, deploymentID string, capacity int32) (*models.Deployment, error)

	// DeleteDeployment deletes a deployment with proper validation
	DeleteDeployment(ctx context.Context, deploymentID string) error

	// WaitForDeployment waits for a deployment to become active
	WaitForDeployment(ctx context.Context, deploymentID string, timeoutSeconds int) (*models.Deployment, error)
}
