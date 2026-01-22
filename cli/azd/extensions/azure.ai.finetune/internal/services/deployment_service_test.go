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

// MockModelDeploymentProvider is a mock implementation for testing
type MockModelDeploymentProvider struct {
	DeployModelFunc         func(ctx context.Context, req *models.DeploymentRequest) (*models.DeployModelResult, error)
	GetDeploymentStatusFunc func(ctx context.Context, deploymentID string) (*models.Deployment, error)
	ListDeploymentsFunc     func(ctx context.Context, limit int, after string) ([]*models.Deployment, error)
	UpdateDeploymentFunc    func(ctx context.Context, deploymentID string, capacity int32) (*models.Deployment, error)
	DeleteDeploymentFunc    func(ctx context.Context, deploymentID string) error
}

func (m *MockModelDeploymentProvider) DeployModel(ctx context.Context, req *models.DeploymentRequest) (*models.DeployModelResult, error) {
	if m.DeployModelFunc != nil {
		return m.DeployModelFunc(ctx, req)
	}
	return nil, errors.New("not implemented")
}

func (m *MockModelDeploymentProvider) GetDeploymentStatus(ctx context.Context, deploymentID string) (*models.Deployment, error) {
	if m.GetDeploymentStatusFunc != nil {
		return m.GetDeploymentStatusFunc(ctx, deploymentID)
	}
	return nil, nil
}

func (m *MockModelDeploymentProvider) ListDeployments(ctx context.Context, limit int, after string) ([]*models.Deployment, error) {
	if m.ListDeploymentsFunc != nil {
		return m.ListDeploymentsFunc(ctx, limit, after)
	}
	return nil, nil
}

func (m *MockModelDeploymentProvider) UpdateDeployment(ctx context.Context, deploymentID string, capacity int32) (*models.Deployment, error) {
	if m.UpdateDeploymentFunc != nil {
		return m.UpdateDeploymentFunc(ctx, deploymentID, capacity)
	}
	return nil, nil
}

func (m *MockModelDeploymentProvider) DeleteDeployment(ctx context.Context, deploymentID string) error {
	if m.DeleteDeploymentFunc != nil {
		return m.DeleteDeploymentFunc(ctx, deploymentID)
	}
	return nil
}

// newTestDeploymentService creates a deployment service with mocks for testing
func newTestDeploymentService(
	deployProvider *MockModelDeploymentProvider,
	ftProvider *MockFineTuningProvider,
	stateStore StateStore,
) *deploymentServiceImpl {
	return &deploymentServiceImpl{
		provider:   deployProvider,
		ftProvider: ftProvider,
		stateStore: stateStore,
	}
}

func TestDeploymentService_DeployModel_NilRequest(t *testing.T) {
	service := newTestDeploymentService(nil, nil, nil)

	result, err := service.DeployModel(context.Background(), nil)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "deployment request cannot be nil")
}

func TestDeploymentService_DeployModel_MissingJobID(t *testing.T) {
	service := newTestDeploymentService(nil, nil, nil)

	req := &models.DeploymentConfig{
		DeploymentName: "my-deployment",
	}

	result, err := service.DeployModel(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "JobID and DeploymentName must be provided")
}

func TestDeploymentService_DeployModel_MissingDeploymentName(t *testing.T) {
	service := newTestDeploymentService(nil, nil, nil)

	req := &models.DeploymentConfig{
		JobID: "job-123",
	}

	result, err := service.DeployModel(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "JobID and DeploymentName must be provided")
}

func TestDeploymentService_DeployModel_BothRequired(t *testing.T) {
	service := newTestDeploymentService(nil, nil, nil)

	req := &models.DeploymentConfig{
		// Both JobID and DeploymentName are empty
	}

	result, err := service.DeployModel(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "JobID and DeploymentName must be provided")
}

func TestDeploymentService_DeployModel_JobDetailsError(t *testing.T) {
	mockFTProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			return nil, errors.New("job not found")
		},
	}
	service := newTestDeploymentService(nil, mockFTProvider, nil)

	req := &models.DeploymentConfig{
		JobID:          "job-123",
		DeploymentName: "my-deployment",
	}

	result, err := service.DeployModel(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "failed to get fine-tuning job details")
}

func TestDeploymentService_DeployModel_NilJobDetails(t *testing.T) {
	mockFTProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			return nil, nil
		},
	}
	service := newTestDeploymentService(nil, mockFTProvider, nil)

	req := &models.DeploymentConfig{
		JobID:          "job-123",
		DeploymentName: "my-deployment",
	}

	result, err := service.DeployModel(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "fine-tuned model not found for job ID")
}

func TestDeploymentService_DeployModel_EmptyFineTunedModel(t *testing.T) {
	mockFTProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			return &models.FineTuningJobDetail{
				ID:             jobID,
				FineTunedModel: "", // Empty - job not complete
			}, nil
		},
	}
	service := newTestDeploymentService(nil, mockFTProvider, nil)

	req := &models.DeploymentConfig{
		JobID:          "job-123",
		DeploymentName: "my-deployment",
	}

	result, err := service.DeployModel(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "fine-tuned model not found for job ID")
}

func TestDeploymentService_DeployModel_ProviderError(t *testing.T) {
	mockFTProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			return &models.FineTuningJobDetail{
				ID:             jobID,
				FineTunedModel: "ft:gpt-4o-mini:my-org::abc123",
			}, nil
		},
	}
	mockDeployProvider := &MockModelDeploymentProvider{
		DeployModelFunc: func(ctx context.Context, req *models.DeploymentRequest) (*models.DeployModelResult, error) {
			return nil, errors.New("deployment failed: quota exceeded")
		},
	}
	service := newTestDeploymentService(mockDeployProvider, mockFTProvider, nil)

	req := &models.DeploymentConfig{
		JobID:          "job-123",
		DeploymentName: "my-deployment",
	}

	result, err := service.DeployModel(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "failed to deploy model")
}

func TestDeploymentService_DeployModel_Success(t *testing.T) {
	expectedResult := &models.DeployModelResult{
		Status:  "succeeded",
		Message: "Model deployed successfully",
		Deployment: models.Deployment{
			ID:             "deploy-123",
			Name:           "my-deployment",
			Status:         models.DeploymentActive,
			FineTunedModel: "ft:gpt-4o-mini:my-org::abc123",
		},
	}

	mockFTProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			require.Equal(t, "job-123", jobID)
			return &models.FineTuningJobDetail{
				ID:             jobID,
				FineTunedModel: "ft:gpt-4o-mini:my-org::abc123",
			}, nil
		},
	}
	mockDeployProvider := &MockModelDeploymentProvider{
		DeployModelFunc: func(ctx context.Context, req *models.DeploymentRequest) (*models.DeployModelResult, error) {
			// Verify the request was properly constructed
			require.Equal(t, "ft:gpt-4o-mini:my-org::abc123", req.ModelName)
			require.Equal(t, "my-deployment", req.DeploymentName)
			require.Equal(t, "Standard", req.SKU)
			require.Equal(t, int32(10), req.Capacity)
			return expectedResult, nil
		},
	}
	service := newTestDeploymentService(mockDeployProvider, mockFTProvider, nil)

	req := &models.DeploymentConfig{
		JobID:          "job-123",
		DeploymentName: "my-deployment",
		SKU:            "Standard",
		Capacity:       10,
	}

	result, err := service.DeployModel(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, expectedResult.Status, result.Status)
	require.Equal(t, expectedResult.Deployment.ID, result.Deployment.ID)
}

func TestDeploymentService_DeployModel_PassesAllConfigFields(t *testing.T) {
	var capturedRequest *models.DeploymentRequest

	mockFTProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			return &models.FineTuningJobDetail{
				ID:             jobID,
				FineTunedModel: "ft:gpt-4o-mini:my-org::abc123",
			}, nil
		},
	}
	mockDeployProvider := &MockModelDeploymentProvider{
		DeployModelFunc: func(ctx context.Context, req *models.DeploymentRequest) (*models.DeployModelResult, error) {
			capturedRequest = req
			return &models.DeployModelResult{Status: "succeeded"}, nil
		},
	}
	service := newTestDeploymentService(mockDeployProvider, mockFTProvider, nil)

	req := &models.DeploymentConfig{
		JobID:             "job-123",
		DeploymentName:    "my-deployment",
		SKU:               "Standard",
		Capacity:          20,
		SubscriptionID:    "sub-456",
		ResourceGroup:     "my-rg",
		AccountName:       "my-account",
		TenantID:          "tenant-789",
		Version:           "1",
		ModelFormat:       "OpenAI",
		WaitForCompletion: true,
	}

	_, err := service.DeployModel(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, capturedRequest)

	// Verify all fields were passed through
	require.Equal(t, "ft:gpt-4o-mini:my-org::abc123", capturedRequest.ModelName)
	require.Equal(t, "my-deployment", capturedRequest.DeploymentName)
	require.Equal(t, "Standard", capturedRequest.SKU)
	require.Equal(t, int32(20), capturedRequest.Capacity)
	require.Equal(t, "sub-456", capturedRequest.SubscriptionID)
	require.Equal(t, "my-rg", capturedRequest.ResourceGroup)
	require.Equal(t, "my-account", capturedRequest.AccountName)
	require.Equal(t, "tenant-789", capturedRequest.TenantID)
	require.Equal(t, "1", capturedRequest.Version)
	require.Equal(t, "OpenAI", capturedRequest.ModelFormat)
	require.True(t, capturedRequest.WaitForCompletion)
}

func TestNewDeploymentService(t *testing.T) {
	mockDeployProvider := &MockModelDeploymentProvider{}
	mockFTProvider := &MockFineTuningProvider{}
	mockStateStore := &MockStateStore{}

	service := NewDeploymentService(mockDeployProvider, mockFTProvider, mockStateStore)

	require.NotNil(t, service)
}

func TestDeploymentService_DeployModel_WaitForCompletionFalse(t *testing.T) {
	var capturedRequest *models.DeploymentRequest

	mockFTProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			return &models.FineTuningJobDetail{
				ID:             jobID,
				FineTunedModel: "ft:gpt-4o-mini:my-org::abc123",
			}, nil
		},
	}
	mockDeployProvider := &MockModelDeploymentProvider{
		DeployModelFunc: func(ctx context.Context, req *models.DeploymentRequest) (*models.DeployModelResult, error) {
			capturedRequest = req
			return &models.DeployModelResult{
				Status:  "pending",
				Message: "Deployment started",
			}, nil
		},
	}
	service := newTestDeploymentService(mockDeployProvider, mockFTProvider, nil)

	req := &models.DeploymentConfig{
		JobID:             "job-123",
		DeploymentName:    "my-deployment",
		WaitForCompletion: false, // Explicitly set to false
	}

	result, err := service.DeployModel(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, capturedRequest)
	require.False(t, capturedRequest.WaitForCompletion)
	require.Equal(t, "pending", result.Status)
}

func TestDeploymentService_DeployModel_DefaultConfigValues(t *testing.T) {
	var capturedRequest *models.DeploymentRequest

	mockFTProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			return &models.FineTuningJobDetail{
				ID:             jobID,
				FineTunedModel: "ft:gpt-4",
			}, nil
		},
	}
	mockDeployProvider := &MockModelDeploymentProvider{
		DeployModelFunc: func(ctx context.Context, req *models.DeploymentRequest) (*models.DeployModelResult, error) {
			capturedRequest = req
			return &models.DeployModelResult{Status: "succeeded"}, nil
		},
	}
	service := newTestDeploymentService(mockDeployProvider, mockFTProvider, nil)

	// Minimal config with only required fields
	req := &models.DeploymentConfig{
		JobID:          "job-456",
		DeploymentName: "minimal-deployment",
	}

	_, err := service.DeployModel(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, capturedRequest)
	// Verify model name was extracted from job details
	require.Equal(t, "ft:gpt-4", capturedRequest.ModelName)
	// Optional fields should be empty/zero
	require.Empty(t, capturedRequest.SKU)
	require.Equal(t, int32(0), capturedRequest.Capacity)
}

func TestDeploymentService_DeployModel_JobNotSucceeded(t *testing.T) {
	// Test when job exists but doesn't have a fine-tuned model yet (still running)
	mockFTProvider := &MockFineTuningProvider{
		GetFineTuningJobDetailsFunc: func(ctx context.Context, jobID string) (*models.FineTuningJobDetail, error) {
			return &models.FineTuningJobDetail{
				ID:             jobID,
				Status:         models.StatusRunning,
				FineTunedModel: "", // Empty because job hasn't finished
			}, nil
		},
	}
	service := newTestDeploymentService(nil, mockFTProvider, nil)

	req := &models.DeploymentConfig{
		JobID:          "running-job",
		DeploymentName: "my-deployment",
	}

	result, err := service.DeployModel(context.Background(), req)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "fine-tuned model not found")
}
