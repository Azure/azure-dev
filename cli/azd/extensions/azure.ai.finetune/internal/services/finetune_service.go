// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package services

import (
	"context"
	"fmt"
	"os"

	"azure.ai.finetune/internal/providers"
	"azure.ai.finetune/internal/providers/factory"
	"azure.ai.finetune/internal/utils"
	"azure.ai.finetune/pkg/models"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

// Ensure fineTuningServiceImpl implements FineTuningService interface
var _ FineTuningService = (*fineTuningServiceImpl)(nil)

// fineTuningServiceImpl implements the FineTuningService interface
type fineTuningServiceImpl struct {
	azdClient  *azdext.AzdClient
	provider   providers.FineTuningProvider
	stateStore StateStore
}

// NewFineTuningService creates a new instance of FineTuningService
func NewFineTuningService(ctx context.Context, azdClient *azdext.AzdClient, stateStore StateStore) (FineTuningService, error) {
	provider, err := factory.NewFineTuningProvider(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize fine-tuning service: %w", err)
	}

	return &fineTuningServiceImpl{
		azdClient:  azdClient,
		provider:   provider,
		stateStore: stateStore,
	}, nil
}

// CreateFineTuningJob creates a new fine-tuning job with business validation
func (s *fineTuningServiceImpl) CreateFineTuningJob(ctx context.Context, req *models.CreateFineTuningRequest) (*models.FineTuningJob, error) {
	// Validate request
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}
	if req.BaseModel == "" {
		return nil, fmt.Errorf("base model is required")
	}
	if req.TrainingFile == "" {
		return nil, fmt.Errorf("training file is required")
	}

	if utils.IsLocalFilePath(req.TrainingFile) {
		color.Green("\nuploading training file...")

		trainingDataID, err := s.UploadFile(ctx, utils.GetLocalFilePath(req.TrainingFile))
		if err != nil {
			return nil, fmt.Errorf("failed to upload training file: %w", err)
		}
		req.TrainingFile = trainingDataID
	} else {
		color.Yellow("\nProvided training file is non-local, skipping upload...")
	}

	// Upload validation file if provided
	if req.ValidationFile != nil && *req.ValidationFile != "" {
		if utils.IsLocalFilePath(*req.ValidationFile) {
			color.Green("\nuploading validation file...")
			validationDataID, err := s.UploadFile(ctx, utils.GetLocalFilePath(*req.ValidationFile))
			if err != nil {
				return nil, fmt.Errorf("failed to upload validation file: %w", err)
			}
			req.ValidationFile = &validationDataID
		} else {
			color.Yellow("\nProvided validation file is non-local, skipping upload...")
		}
	}

	// Call provider with retry logic
	var job *models.FineTuningJob
	err := utils.RetryOperation(ctx, utils.DefaultRetryConfig(), func() error {
		var err error
		job, err = s.provider.CreateFineTuningJob(ctx, req)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create fine-tuning job: %w", err)
	}

	// Persist job to state store if available
	if s.stateStore != nil {
		if err := s.stateStore.SaveJob(ctx, job); err != nil {
			return nil, fmt.Errorf("failed to persist job: %w", err)
		}
	}

	return job, nil
}

// GetFineTuningStatus retrieves the current status of a job
func (s *fineTuningServiceImpl) GetFineTuningStatus(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	// TODO: Implement
	return nil, nil
}

// ListFineTuningJobs lists all fine-tuning jobs for the user
func (s *fineTuningServiceImpl) ListFineTuningJobs(ctx context.Context, limit int, after string) ([]*models.FineTuningJob, error) {
	var jobs []*models.FineTuningJob

	// Use retry utility for list operation
	err := utils.RetryOperation(ctx, utils.DefaultRetryConfig(), func() error {
		var err error
		jobs, err = s.provider.ListFineTuningJobs(ctx, limit, after)
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list fine-tuning jobs: %w", err)
	}

	return jobs, nil
}

// GetFineTuningJobDetails retrieves detailed information about a job
func (s *fineTuningServiceImpl) GetFineTuningJobDetails(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
	var jobDetail *models.FineTuningJobDetail

	// Use retry utility for job detail operation
	err := utils.RetryOperation(ctx, utils.DefaultRetryConfig(), func() error {
		var err error
		jobDetail, err = s.provider.GetFineTuningJobDetails(ctx, jobID)
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get job details: %w", err)
	}

	return jobDetail, nil
}

// GetJobEvents retrieves events for a job with filtering and pagination
func (s *fineTuningServiceImpl) GetJobEvents(ctx context.Context, jobID string) (*models.JobEventsList, error) {
	var eventsList *models.JobEventsList

	// Use retry utility for job events operation
	err := utils.RetryOperation(ctx, utils.DefaultRetryConfig(), func() error {
		var err error
		eventsList, err = s.provider.GetJobEvents(ctx, jobID)
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get job events: %w", err)
	}

	return eventsList, nil
}

// GetJobCheckpoints retrieves checkpoints for a job with pagination
func (s *fineTuningServiceImpl) GetJobCheckpoints(ctx context.Context, jobID string) (*models.JobCheckpointsList, error) {
	var checkpointList *models.JobCheckpointsList

	// Use retry utility for job checkpoints operation
	err := utils.RetryOperation(ctx, utils.DefaultRetryConfig(), func() error {
		var err error
		checkpointList, err = s.provider.GetJobCheckpoints(ctx, jobID)
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get job checkpoints: %w", err)
	}

	return checkpointList, nil
}

// executeJobAction performs a job state change action (pause, resume, cancel) with retry logic
func (s *fineTuningServiceImpl) executeJobAction(ctx context.Context, jobID string, action models.JobAction) (*models.FineTuningJob, error) {
	var job *models.FineTuningJob

	err := utils.RetryOperation(ctx, utils.DefaultRetryConfig(), func() error {
		var err error
		switch action {
		case models.JobActionPause:
			job, err = s.provider.PauseJob(ctx, jobID)
		case models.JobActionResume:
			job, err = s.provider.ResumeJob(ctx, jobID)
		case models.JobActionCancel:
			job, err = s.provider.CancelJob(ctx, jobID)
		}
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to %s fine-tuning job: %w", action, err)
	}
	return job, nil
}

// PauseJob pauses a running job (if applicable)
func (s *fineTuningServiceImpl) PauseJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	return s.executeJobAction(ctx, jobID, models.JobActionPause)
}

// ResumeJob resumes a paused job (if applicable)
func (s *fineTuningServiceImpl) ResumeJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	return s.executeJobAction(ctx, jobID, models.JobActionResume)
}

// CancelJob cancels a job with proper state validation
func (s *fineTuningServiceImpl) CancelJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	return s.executeJobAction(ctx, jobID, models.JobActionCancel)
}

// UploadFile uploads and validates a file
func (s *fineTuningServiceImpl) UploadFile(ctx context.Context, filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("file path cannot be empty")
	}
	uploadedFileId, err := s.uploadFile(ctx, filePath)
	if err != nil || uploadedFileId == "" {
		return "", fmt.Errorf("failed to upload file: %w", err)
	}
	return uploadedFileId, nil
}

func (s *fineTuningServiceImpl) uploadFile(ctx context.Context, filePath string) (string, error) {
	// validate file existence
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file does not exist: %s", filePath)
		}
		return "", fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}
	if fileInfo.IsDir() {
		return "", fmt.Errorf("path is a directory, not a file: %s", filePath)
	}

	// upload file with retry
	uploadedFileId := ""
	err = utils.RetryOperation(ctx, utils.DefaultRetryConfig(), func() error {
		var err error
		uploadedFileId, err = s.provider.UploadFile(ctx, filePath)
		return err
	})
	return uploadedFileId, err
}

// PollJobUntilCompletion polls a job until it completes or fails
func (s *fineTuningServiceImpl) PollJobUntilCompletion(ctx context.Context, jobID string, intervalSeconds int) (*models.FineTuningJob, error) {
	// TODO: Implement
	return nil, nil
}
