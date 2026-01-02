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
	Utils "azure.ai.finetune/internal/utils"
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

	if Utils.IsLocalFilePath(req.TrainingFile) {
		color.Green("uploading training file...")

		trainingDataID, err := s.UploadFile(ctx, Utils.GetLocalFilePath(req.TrainingFile))
		if err != nil {
			return nil, fmt.Errorf("failed to upload training file: %w", err)
		}
		req.TrainingFile = trainingDataID
	} else {
		color.Yellow("Provided training file is non-local, skipping upload...")
	}

	// Upload validation file if provided
	if req.ValidationFile != nil && *req.ValidationFile != "" {
		if Utils.IsLocalFilePath(*req.ValidationFile) {
			color.Green("\nuploading validation file...")
			validationDataID, err := s.UploadFile(ctx, Utils.GetLocalFilePath(*req.ValidationFile))
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
	// TODO: Implement
	return nil, nil
}

// GetJobEvents retrieves events for a job with filtering and pagination
func (s *fineTuningServiceImpl) GetJobEvents(ctx context.Context, jobID string, limit int, after string) ([]*models.JobEvent, error) {
	// TODO: Implement
	return nil, nil
}

// GetJobCheckpoints retrieves checkpoints for a job with pagination
func (s *fineTuningServiceImpl) GetJobCheckpoints(ctx context.Context, jobID string, limit int, after string) ([]*models.JobCheckpoint, error) {
	// TODO: Implement
	return nil, nil
}

// PauseJob pauses a running job (if applicable)
func (s *fineTuningServiceImpl) PauseJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	// TODO: Implement
	return nil, nil
}

// ResumeJob resumes a paused job (if applicable)
func (s *fineTuningServiceImpl) ResumeJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	// TODO: Implement
	return nil, nil
}

// CancelJob cancels a job with proper state validation
func (s *fineTuningServiceImpl) CancelJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	// TODO: Implement
	return nil, nil
}

// UploadFile uploads and validates a file
func (s *fineTuningServiceImpl) UploadFile(ctx context.Context, filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("file path cannot be empty")
	}
	uploadedFileId, err := s._uploadFile(ctx, filePath)
	if err != nil || uploadedFileId == "" {
		return "", fmt.Errorf("failed to upload file: %w", err)
	}
	return uploadedFileId, nil
}

func (s *fineTuningServiceImpl) _uploadFile(ctx context.Context, filePath string) (string, error) {
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
