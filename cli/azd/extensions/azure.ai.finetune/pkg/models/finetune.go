// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import "time"

// JobStatus represents the status of a fine-tuning job
type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusQueued    JobStatus = "queued"
	StatusRunning   JobStatus = "running"
	StatusSucceeded JobStatus = "succeeded"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
	StatusPaused    JobStatus = "paused"
)

// FineTuningJob represents a vendor-agnostic fine-tuning job
type FineTuningJob struct {
	// Core identification
	ID          string
	VendorJobID string // Vendor-specific ID (e.g., OpenAI's ftjob-xxx)

	// Job details
	Status         JobStatus
	BaseModel      string
	FineTunedModel string

	// Timestamps
	CreatedAt   time.Time
	CompletedAt *time.Time

	// Files
	TrainingFileID   string
	ValidationFileID string

	// Metadata
	VendorMetadata map[string]interface{} // Store vendor-specific details
	ErrorDetails   *ErrorDetail
}

// CreateFineTuningRequest represents a request to create a fine-tuning job
type CreateFineTuningRequest struct {
	BaseModel        string
	TrainingDataID   string
	ValidationDataID string
	Hyperparameters  *Hyperparameters
}

// Hyperparameters represents fine-tuning hyperparameters
type Hyperparameters struct {
	BatchSize              int64
	LearningRateMultiplier float64
	NEpochs                int64
}

// ListFineTuningJobsRequest represents a request to list fine-tuning jobs
type ListFineTuningJobsRequest struct {
	Limit int
	After string
}

// FineTuningJobDetail represents detailed information about a fine-tuning job
type FineTuningJobDetail struct {
	ID              string
	Status          JobStatus
	Model           string
	FineTunedModel  string
	CreatedAt       time.Time
	FinishedAt      time.Time
	Method          string
	TrainingFile    string
	ValidationFile  string
	Hyperparameters *Hyperparameters
	VendorMetadata  map[string]interface{}
}

// JobEvent represents an event associated with a fine-tuning job
type JobEvent struct {
	ID        string
	CreatedAt time.Time
	Level     string
	Message   string
	Data      interface{}
	Type      string
}

type JobEventsListContract struct {
	Data    []JobEvent
	HasMore bool
}


// JobCheckpoint represents a checkpoint of a fine-tuning job
type JobCheckpoint struct {
	ID                       string
	CreatedAt                time.Time
	FineTunedModelCheckpoint string
	Metrics                  *CheckpointMetrics
	FineTuningJobID          string
	StepNumber               int64
}

type JobCheckpointsListContract struct {
	Data []JobCheckpoint
	HasMore bool
}

// CheckpointMetrics represents metrics for a checkpoint
type CheckpointMetrics struct {
	FullValidLoss              float64
	FullValidMeanTokenAccuracy float64
}
