// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package services

import (
	"context"

	"azure.ai.finetune/pkg/models"
)

// StateStore defines the interface for persisting job state
// This allows tracking jobs across CLI sessions
type StateStore interface {
	// SaveJob persists a job to local storage
	SaveJob(ctx context.Context, job *models.FineTuningJob) error

	// GetJob retrieves a job from local storage
	GetJob(ctx context.Context, jobID string) (*models.FineTuningJob, error)

	// ListJobs lists all locally tracked jobs
	ListJobs(ctx context.Context) ([]*models.FineTuningJob, error)

	// UpdateJobStatus updates the status of a tracked job
	UpdateJobStatus(ctx context.Context, jobID string, status models.JobStatus) error

	// DeleteJob removes a job from local storage
	DeleteJob(ctx context.Context, jobID string) error

	// SaveDeployment persists a deployment to local storage
	SaveDeployment(ctx context.Context, deployment *models.Deployment) error

	// GetDeployment retrieves a deployment from local storage
	GetDeployment(ctx context.Context, deploymentID string) (*models.Deployment, error)

	// ListDeployments lists all locally tracked deployments
	ListDeployments(ctx context.Context) ([]*models.Deployment, error)

	// UpdateDeploymentStatus updates the status of a tracked deployment
	UpdateDeploymentStatus(ctx context.Context, deploymentID string, status models.DeploymentStatus) error

	// DeleteDeployment removes a deployment from local storage
	DeleteDeployment(ctx context.Context, deploymentID string) error
}

// ErrorTransformer defines the interface for transforming vendor-specific errors
// to standardized error details
type ErrorTransformer interface {
	// TransformError converts a vendor-specific error to a standardized ErrorDetail
	TransformError(vendorError error, vendorCode string) *models.ErrorDetail
}
