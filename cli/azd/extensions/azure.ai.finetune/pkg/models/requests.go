// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// PauseJobRequest represents a request to pause a fine-tuning job
type PauseJobRequest struct {
	JobID string
}

// ResumeJobRequest represents a request to resume a fine-tuning job
type ResumeJobRequest struct {
	JobID string
}

// CancelJobRequest represents a request to cancel a fine-tuning job
type CancelJobRequest struct {
	JobID string
}

// GetJobDetailsRequest represents a request to get job details
type GetJobDetailsRequest struct {
	JobID string
}

// GetJobEventsRequest represents a request to list job events
type GetJobEventsRequest struct {
	JobID string
	Limit int
	After string
}

// GetJobCheckpointsRequest represents a request to list job checkpoints
type GetJobCheckpointsRequest struct {
	JobID string
	Limit int
	After string
}

// ListDeploymentsRequest represents a request to list deployments
type ListDeploymentsRequest struct {
	Limit int
	After string
}

// GetDeploymentRequest represents a request to get deployment details
type GetDeploymentRequest struct {
	DeploymentID string
}

// DeleteDeploymentRequest represents a request to delete a deployment
type DeleteDeploymentRequest struct {
	DeploymentID string
}

// UpdateDeploymentRequest represents a request to update a deployment
type UpdateDeploymentRequest struct {
	DeploymentID string
	Capacity     int32
}
