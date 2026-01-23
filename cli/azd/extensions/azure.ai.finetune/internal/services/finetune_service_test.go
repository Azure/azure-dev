// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package services

import (
	"context"
	"errors"
	"testing"

	"azure.ai.finetune/pkg/models"
	"github.com/stretchr/testify/require"
)

// MockFineTuningProvider is a mock implementation of the FineTuningProvider interface for testing
type MockFineTuningProvider struct {
	CreateFineTuningJobFunc     func(ctx context.Context, req *models.CreateFineTuningRequest) (*models.FineTuningJob, error)
	GetFineTuningStatusFunc     func(ctx context.Context, jobID string) (*models.FineTuningJob, error)
	ListFineTuningJobsFunc      func(ctx context.Context, limit int, after string) ([]*models.FineTuningJob, error)
	GetFineTuningJobDetailsFunc func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error)
	GetJobEventsFunc            func(ctx context.Context, jobID string) (*models.JobEventsList, error)
	GetJobCheckpointsFunc       func(ctx context.Context, jobID string) (*models.JobCheckpointsList, error)
	PauseJobFunc                func(ctx context.Context, jobID string) (*models.FineTuningJob, error)
	ResumeJobFunc               func(ctx context.Context, jobID string) (*models.FineTuningJob, error)
	CancelJobFunc               func(ctx context.Context, jobID string) (*models.FineTuningJob, error)
	UploadFileFunc              func(ctx context.Context, filePath string) (string, error)
	GetUploadedFileFunc         func(ctx context.Context, fileID string) (interface{}, error)
}

func (m *MockFineTuningProvider) CreateFineTuningJob(ctx context.Context, req *models.CreateFineTuningRequest) (*models.FineTuningJob, error) {
	if m.CreateFineTuningJobFunc != nil {
		return m.CreateFineTuningJobFunc(ctx, req)
	}
	return nil, errors.New("not implemented")
}

func (m *MockFineTuningProvider) GetFineTuningStatus(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	if m.GetFineTuningStatusFunc != nil {
		return m.GetFineTuningStatusFunc(ctx, jobID)
	}
	return nil, errors.New("not implemented")
}

func (m *MockFineTuningProvider) ListFineTuningJobs(ctx context.Context, limit int, after string) ([]*models.FineTuningJob, error) {
	if m.ListFineTuningJobsFunc != nil {
		return m.ListFineTuningJobsFunc(ctx, limit, after)
	}
	return nil, errors.New("not implemented")
}

func (m *MockFineTuningProvider) GetFineTuningJobDetails(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
	if m.GetFineTuningJobDetailsFunc != nil {
		return m.GetFineTuningJobDetailsFunc(ctx, jobID)
	}
	return nil, errors.New("not implemented")
}

func (m *MockFineTuningProvider) GetJobEvents(ctx context.Context, jobID string) (*models.JobEventsList, error) {
	if m.GetJobEventsFunc != nil {
		return m.GetJobEventsFunc(ctx, jobID)
	}
	return nil, errors.New("not implemented")
}

func (m *MockFineTuningProvider) GetJobCheckpoints(ctx context.Context, jobID string) (*models.JobCheckpointsList, error) {
	if m.GetJobCheckpointsFunc != nil {
		return m.GetJobCheckpointsFunc(ctx, jobID)
	}
	return nil, errors.New("not implemented")
}

func (m *MockFineTuningProvider) PauseJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	if m.PauseJobFunc != nil {
		return m.PauseJobFunc(ctx, jobID)
	}
	return nil, errors.New("not implemented")
}

func (m *MockFineTuningProvider) ResumeJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	if m.ResumeJobFunc != nil {
		return m.ResumeJobFunc(ctx, jobID)
	}
	return nil, errors.New("not implemented")
}

func (m *MockFineTuningProvider) CancelJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	if m.CancelJobFunc != nil {
		return m.CancelJobFunc(ctx, jobID)
	}
	return nil, errors.New("not implemented")
}

func (m *MockFineTuningProvider) UploadFile(ctx context.Context, filePath string) (string, error) {
	if m.UploadFileFunc != nil {
		return m.UploadFileFunc(ctx, filePath)
	}
	return "", errors.New("not implemented")
}

func (m *MockFineTuningProvider) GetUploadedFile(ctx context.Context, fileID string) (interface{}, error) {
	if m.GetUploadedFileFunc != nil {
		return m.GetUploadedFileFunc(ctx, fileID)
	}
	return nil, errors.New("not implemented")
}

// MockStateStore is a mock implementation of the StateStore interface for testing
type MockStateStore struct {
	SaveJobFunc                func(ctx context.Context, job *models.FineTuningJob) error
	GetJobFunc                 func(ctx context.Context, jobID string) (*models.FineTuningJob, error)
	ListJobsFunc               func(ctx context.Context) ([]*models.FineTuningJob, error)
	UpdateJobStatusFunc        func(ctx context.Context, jobID string, status models.JobStatus) error
	DeleteJobFunc              func(ctx context.Context, jobID string) error
	SaveDeploymentFunc         func(ctx context.Context, deployment *models.Deployment) error
	GetDeploymentFunc          func(ctx context.Context, deploymentID string) (*models.Deployment, error)
	ListDeploymentsFunc        func(ctx context.Context) ([]*models.Deployment, error)
	UpdateDeploymentStatusFunc func(ctx context.Context, deploymentID string, status models.DeploymentStatus) error
	DeleteDeploymentFunc       func(ctx context.Context, deploymentID string) error
}

func (m *MockStateStore) SaveJob(ctx context.Context, job *models.FineTuningJob) error {
	if m.SaveJobFunc != nil {
		return m.SaveJobFunc(ctx, job)
	}
	return nil
}

func (m *MockStateStore) GetJob(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
	if m.GetJobFunc != nil {
		return m.GetJobFunc(ctx, jobID)
	}
	return nil, nil
}

func (m *MockStateStore) ListJobs(ctx context.Context) ([]*models.FineTuningJob, error) {
	if m.ListJobsFunc != nil {
		return m.ListJobsFunc(ctx)
	}
	return nil, nil
}

func (m *MockStateStore) UpdateJobStatus(ctx context.Context, jobID string, status models.JobStatus) error {
	if m.UpdateJobStatusFunc != nil {
		return m.UpdateJobStatusFunc(ctx, jobID, status)
	}
	return nil
}

func (m *MockStateStore) DeleteJob(ctx context.Context, jobID string) error {
	if m.DeleteJobFunc != nil {
		return m.DeleteJobFunc(ctx, jobID)
	}
	return nil
}

func (m *MockStateStore) SaveDeployment(ctx context.Context, deployment *models.Deployment) error {
	if m.SaveDeploymentFunc != nil {
		return m.SaveDeploymentFunc(ctx, deployment)
	}
	return nil
}

func (m *MockStateStore) GetDeployment(ctx context.Context, deploymentID string) (*models.Deployment, error) {
	if m.GetDeploymentFunc != nil {
		return m.GetDeploymentFunc(ctx, deploymentID)
	}
	return nil, nil
}

func (m *MockStateStore) ListDeployments(ctx context.Context) ([]*models.Deployment, error) {
	if m.ListDeploymentsFunc != nil {
		return m.ListDeploymentsFunc(ctx)
	}
	return nil, nil
}

func (m *MockStateStore) UpdateDeploymentStatus(ctx context.Context, deploymentID string, status models.DeploymentStatus) error {
	if m.UpdateDeploymentStatusFunc != nil {
		return m.UpdateDeploymentStatusFunc(ctx, deploymentID, status)
	}
	return nil
}

func (m *MockStateStore) DeleteDeployment(ctx context.Context, deploymentID string) error {
	if m.DeleteDeploymentFunc != nil {
		return m.DeleteDeploymentFunc(ctx, deploymentID)
	}
	return nil
}

// newTestFineTuningService creates a new FineTuningService with a mock provider for testing
func newTestFineTuningService(provider *MockFineTuningProvider, stateStore StateStore) *fineTuningServiceImpl {
	return &fineTuningServiceImpl{
		azdClient:  nil,
		provider:   provider,
		stateStore: stateStore,
	}
}

func TestFineTuningService_CreateFineTuningJob_NilRequest(t *testing.T) {
	mockProvider := &MockFineTuningProvider{}
	service := newTestFineTuningService(mockProvider, nil)

	job, err := service.CreateFineTuningJob(context.Background(), nil)

	require.Error(t, err)
	require.Nil(t, job)
	require.Contains(t, err.Error(), "request cannot be nil")
}

func TestFineTuningService_CreateFineTuningJob_MissingBaseModel(t *testing.T) {
	mockProvider := &MockFineTuningProvider{}
	service := newTestFineTuningService(mockProvider, nil)

	req := &models.CreateFineTuningRequest{
		TrainingFile: "file-abc123",
	}

	job, err := service.CreateFineTuningJob(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, job)
	require.Contains(t, err.Error(), "base model is required")
}

func TestFineTuningService_CreateFineTuningJob_MissingTrainingFile(t *testing.T) {
	mockProvider := &MockFineTuningProvider{}
	service := newTestFineTuningService(mockProvider, nil)

	req := &models.CreateFineTuningRequest{
		BaseModel: "gpt-4o-mini",
	}

	job, err := service.CreateFineTuningJob(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, job)
	require.Contains(t, err.Error(), "training file is required")
}

func TestFineTuningService_CreateFineTuningJob_Success(t *testing.T) {
	expectedJob := &models.FineTuningJob{
		ID:        "job-123",
		BaseModel: "gpt-4o-mini",
		Status:    models.StatusPending,
	}

	mockProvider := &MockFineTuningProvider{
		CreateFineTuningJobFunc: func(ctx context.Context, req *models.CreateFineTuningRequest) (*models.FineTuningJob, error) {
			return expectedJob, nil
		},
	}
	service := newTestFineTuningService(mockProvider, nil)

	req := &models.CreateFineTuningRequest{
		BaseModel:    "gpt-4o-mini",
		TrainingFile: "file-abc123",
	}

	job, err := service.CreateFineTuningJob(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, expectedJob.ID, job.ID)
	require.Equal(t, expectedJob.BaseModel, job.BaseModel)
	require.Equal(t, expectedJob.Status, job.Status)
}

func TestFineTuningService_CreateFineTuningJob_WithStateStore(t *testing.T) {
	savedJob := (*models.FineTuningJob)(nil)

	expectedJob := &models.FineTuningJob{
		ID:        "job-456",
		BaseModel: "gpt-4o-mini",
		Status:    models.StatusQueued,
	}

	mockProvider := &MockFineTuningProvider{
		CreateFineTuningJobFunc: func(ctx context.Context, req *models.CreateFineTuningRequest) (*models.FineTuningJob, error) {
			return expectedJob, nil
		},
	}

	mockStateStore := &MockStateStore{
		SaveJobFunc: func(ctx context.Context, job *models.FineTuningJob) error {
			savedJob = job
			return nil
		},
	}

	service := newTestFineTuningService(mockProvider, mockStateStore)

	req := &models.CreateFineTuningRequest{
		BaseModel:    "gpt-4o-mini",
		TrainingFile: "file-xyz789",
	}

	job, err := service.CreateFineTuningJob(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, job)
	require.NotNil(t, savedJob)
	require.Equal(t, expectedJob.ID, savedJob.ID)
}

func TestFineTuningService_CreateFineTuningJob_StateStoreFails(t *testing.T) {
	expectedJob := &models.FineTuningJob{
		ID:        "job-789",
		BaseModel: "gpt-4o-mini",
		Status:    models.StatusPending,
	}

	mockProvider := &MockFineTuningProvider{
		CreateFineTuningJobFunc: func(ctx context.Context, req *models.CreateFineTuningRequest) (*models.FineTuningJob, error) {
			return expectedJob, nil
		},
	}

	mockStateStore := &MockStateStore{
		SaveJobFunc: func(ctx context.Context, job *models.FineTuningJob) error {
			return errors.New("state store error")
		},
	}

	service := newTestFineTuningService(mockProvider, mockStateStore)

	req := &models.CreateFineTuningRequest{
		BaseModel:    "gpt-4o-mini",
		TrainingFile: "file-abc123",
	}

	job, err := service.CreateFineTuningJob(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, job)
	require.Contains(t, err.Error(), "failed to persist job")
}

func TestFineTuningService_ListFineTuningJobs_Success(t *testing.T) {
	expectedJobs := []*models.FineTuningJob{
		{ID: "job-1", BaseModel: "gpt-4o-mini", Status: models.StatusSucceeded},
		{ID: "job-2", BaseModel: "gpt-4", Status: models.StatusRunning},
	}

	mockProvider := &MockFineTuningProvider{
		ListFineTuningJobsFunc: func(ctx context.Context, limit int, after string) ([]*models.FineTuningJob, error) {
			return expectedJobs, nil
		},
	}
	service := newTestFineTuningService(mockProvider, nil)

	jobs, err := service.ListFineTuningJobs(context.Background(), 10, "")

	require.NoError(t, err)
	require.Len(t, jobs, 2)
	require.Equal(t, "job-1", jobs[0].ID)
	require.Equal(t, "job-2", jobs[1].ID)
}

func TestFineTuningService_ListFineTuningJobs_Error(t *testing.T) {
	mockProvider := &MockFineTuningProvider{
		ListFineTuningJobsFunc: func(ctx context.Context, limit int, after string) ([]*models.FineTuningJob, error) {
			return nil, errors.New("API error")
		},
	}
	service := newTestFineTuningService(mockProvider, nil)

	jobs, err := service.ListFineTuningJobs(context.Background(), 10, "")

	require.Error(t, err)
	require.Nil(t, jobs)
	require.Contains(t, err.Error(), "failed to list fine-tuning jobs")
}

func TestFineTuningService_GetFineTuningJobDetails_Success(t *testing.T) {
	expectedDetail := &models.FineTuningJobDetail{
		ID:           "job-123",
		Model:        "gpt-4o-mini",
		Status:       models.StatusSucceeded,
		TrainingFile: "file-train",
	}

	mockProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			require.Equal(t, "job-123", jobID)
			return expectedDetail, nil
		},
	}
	service := newTestFineTuningService(mockProvider, nil)

	detail, err := service.GetFineTuningJobDetails(context.Background(), "job-123")

	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Equal(t, expectedDetail.ID, detail.ID)
}

func TestFineTuningService_GetFineTuningJobDetails_Error(t *testing.T) {
	mockProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			return nil, errors.New("job not found")
		},
	}
	service := newTestFineTuningService(mockProvider, nil)

	detail, err := service.GetFineTuningJobDetails(context.Background(), "nonexistent")

	require.Error(t, err)
	require.Nil(t, detail)
	require.Contains(t, err.Error(), "failed to get job details")
}

func TestFineTuningService_GetJobEvents_Success(t *testing.T) {
	expectedEvents := &models.JobEventsList{
		Data: []models.JobEvent{
			{ID: "event-1", Message: "Job started"},
			{ID: "event-2", Message: "Training epoch 1 completed"},
		},
		HasMore: false,
	}

	mockProvider := &MockFineTuningProvider{
		GetJobEventsFunc: func(ctx context.Context, jobID string) (*models.JobEventsList, error) {
			return expectedEvents, nil
		},
	}
	service := newTestFineTuningService(mockProvider, nil)

	events, err := service.GetJobEvents(context.Background(), "job-123")

	require.NoError(t, err)
	require.NotNil(t, events)
	require.Len(t, events.Data, 2)
	require.False(t, events.HasMore)
}

func TestFineTuningService_GetJobCheckpoints_Success(t *testing.T) {
	expectedCheckpoints := &models.JobCheckpointsList{
		Data: []models.JobCheckpoint{
			{ID: "checkpoint-1", StepNumber: 100},
			{ID: "checkpoint-2", StepNumber: 200},
		},
		HasMore: true,
	}

	mockProvider := &MockFineTuningProvider{
		GetJobCheckpointsFunc: func(ctx context.Context, jobID string) (*models.JobCheckpointsList, error) {
			return expectedCheckpoints, nil
		},
	}
	service := newTestFineTuningService(mockProvider, nil)

	checkpoints, err := service.GetJobCheckpoints(context.Background(), "job-123")

	require.NoError(t, err)
	require.NotNil(t, checkpoints)
	require.Len(t, checkpoints.Data, 2)
	require.True(t, checkpoints.HasMore)
}

func TestFineTuningService_PauseJob_Success(t *testing.T) {
	expectedJob := &models.FineTuningJob{
		ID:     "job-123",
		Status: models.StatusPaused,
	}

	mockProvider := &MockFineTuningProvider{
		PauseJobFunc: func(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
			return expectedJob, nil
		},
	}
	service := newTestFineTuningService(mockProvider, nil)

	job, err := service.PauseJob(context.Background(), "job-123")

	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, models.StatusPaused, job.Status)
}

func TestFineTuningService_ResumeJob_Success(t *testing.T) {
	expectedJob := &models.FineTuningJob{
		ID:     "job-123",
		Status: models.StatusRunning,
	}

	mockProvider := &MockFineTuningProvider{
		ResumeJobFunc: func(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
			return expectedJob, nil
		},
	}
	service := newTestFineTuningService(mockProvider, nil)

	job, err := service.ResumeJob(context.Background(), "job-123")

	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, models.StatusRunning, job.Status)
}

func TestFineTuningService_CancelJob_Success(t *testing.T) {
	expectedJob := &models.FineTuningJob{
		ID:     "job-123",
		Status: models.StatusCancelled,
	}

	mockProvider := &MockFineTuningProvider{
		CancelJobFunc: func(ctx context.Context, jobID string) (*models.FineTuningJob, error) {
			return expectedJob, nil
		},
	}
	service := newTestFineTuningService(mockProvider, nil)

	job, err := service.CancelJob(context.Background(), "job-123")

	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, models.StatusCancelled, job.Status)
}

func TestFineTuningService_UploadFile_EmptyPath(t *testing.T) {
	mockProvider := &MockFineTuningProvider{}
	service := newTestFineTuningService(mockProvider, nil)

	fileID, err := service.UploadFile(context.Background(), "")

	require.Error(t, err)
	require.Empty(t, fileID)
	require.Contains(t, err.Error(), "file path cannot be empty")
}
