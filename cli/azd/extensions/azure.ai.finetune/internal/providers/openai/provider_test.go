// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package openai

import (
	"context"
	"testing"

	"azure.ai.finetune/internal/providers"
	"azure.ai.finetune/pkg/models"
	"github.com/stretchr/testify/require"
)

// TestOpenAIProvider_ImplementsInterfaces verifies that OpenAIProvider
// correctly implements both required provider interfaces
func TestOpenAIProvider_ImplementsInterfaces(t *testing.T) {
	t.Run("ImplementsFineTuningProvider", func(t *testing.T) {
		var _ providers.FineTuningProvider = (*OpenAIProvider)(nil)
	})

	t.Run("ImplementsModelDeploymentProvider", func(t *testing.T) {
		var _ providers.ModelDeploymentProvider = (*OpenAIProvider)(nil)
	})
}

func TestNewOpenAIProvider(t *testing.T) {
	t.Run("WithNilClient", func(t *testing.T) {
		provider := NewOpenAIProvider(nil)

		require.NotNil(t, provider)
		require.Nil(t, provider.client)
	})

	t.Run("ReturnsNonNilProvider", func(t *testing.T) {
		provider := NewOpenAIProvider(nil)

		require.NotNil(t, provider)
		require.IsType(t, &OpenAIProvider{}, provider)
	})
}

func TestOpenAIProvider_UploadFile_Validation(t *testing.T) {
	// Tests input validation that prevents invalid API calls
	provider := NewOpenAIProvider(nil)

	t.Run("EmptyPath_ReturnsError", func(t *testing.T) {
		fileID, err := provider.UploadFile(context.Background(), "")

		require.Error(t, err)
		require.Empty(t, fileID)
		require.Contains(t, err.Error(), "file path cannot be empty")
	})

	t.Run("WhitespaceOnlyPath_ReturnsError", func(t *testing.T) {
		fileID, err := provider.UploadFile(context.Background(), "   ")

		// Note: Current implementation doesn't trim whitespace, so this tests actual behavior
		// which attempts to open a whitespace filename (will fail at file open)
		require.Error(t, err)
		require.Empty(t, fileID)
	})

	t.Run("NonExistentFile_ReturnsError", func(t *testing.T) {
		fileID, err := provider.UploadFile(context.Background(), "/nonexistent/path/to/file.jsonl")

		require.Error(t, err)
		require.Empty(t, fileID)
		require.Contains(t, err.Error(), "failed to open file")
	})
}

// TestOpenAIProvider_MethodSignatures verifies all provider methods have correct signatures
// This acts as a compile-time check that the interface is properly implemented
func TestOpenAIProvider_MethodSignatures(t *testing.T) {
	provider := NewOpenAIProvider(nil)

	// Fine-tuning operations - verify method signatures match interface
	t.Run("CreateFineTuningJob_Signature", func(t *testing.T) {
		var createFunc func(context.Context, *models.CreateFineTuningRequest) (*models.FineTuningJob, error)
		createFunc = provider.CreateFineTuningJob
		require.NotNil(t, createFunc)
	})

	t.Run("GetFineTuningStatus_Signature", func(t *testing.T) {
		var statusFunc func(context.Context, string) (*models.FineTuningJob, error)
		statusFunc = provider.GetFineTuningStatus
		require.NotNil(t, statusFunc)
	})

	t.Run("ListFineTuningJobs_Signature", func(t *testing.T) {
		var listFunc func(context.Context, int, string) ([]*models.FineTuningJob, error)
		listFunc = provider.ListFineTuningJobs
		require.NotNil(t, listFunc)
	})

	t.Run("GetFineTuningJobDetails_Signature", func(t *testing.T) {
		var detailsFunc func(context.Context, string) (*models.FineTuningJobDetail, error)
		detailsFunc = provider.GetFineTuningJobDetails
		require.NotNil(t, detailsFunc)
	})

	t.Run("GetJobEvents_Signature", func(t *testing.T) {
		var eventsFunc func(context.Context, string) (*models.JobEventsList, error)
		eventsFunc = provider.GetJobEvents
		require.NotNil(t, eventsFunc)
	})

	t.Run("GetJobCheckpoints_Signature", func(t *testing.T) {
		var checkpointsFunc func(context.Context, string) (*models.JobCheckpointsList, error)
		checkpointsFunc = provider.GetJobCheckpoints
		require.NotNil(t, checkpointsFunc)
	})

	t.Run("PauseJob_Signature", func(t *testing.T) {
		var pauseFunc func(context.Context, string) (*models.FineTuningJob, error)
		pauseFunc = provider.PauseJob
		require.NotNil(t, pauseFunc)
	})

	t.Run("ResumeJob_Signature", func(t *testing.T) {
		var resumeFunc func(context.Context, string) (*models.FineTuningJob, error)
		resumeFunc = provider.ResumeJob
		require.NotNil(t, resumeFunc)
	})

	t.Run("CancelJob_Signature", func(t *testing.T) {
		var cancelFunc func(context.Context, string) (*models.FineTuningJob, error)
		cancelFunc = provider.CancelJob
		require.NotNil(t, cancelFunc)
	})

	t.Run("UploadFile_Signature", func(t *testing.T) {
		var uploadFunc func(context.Context, string) (string, error)
		uploadFunc = provider.UploadFile
		require.NotNil(t, uploadFunc)
	})

	t.Run("GetUploadedFile_Signature", func(t *testing.T) {
		var getFileFunc func(context.Context, string) (interface{}, error)
		getFileFunc = provider.GetUploadedFile
		require.NotNil(t, getFileFunc)
	})

	// Model deployment operations
	t.Run("DeployModel_Signature", func(t *testing.T) {
		var deployFunc func(context.Context, *models.DeploymentRequest) (*models.DeployModelResult, error)
		deployFunc = provider.DeployModel
		require.NotNil(t, deployFunc)
	})

	t.Run("GetDeploymentStatus_Signature", func(t *testing.T) {
		var statusFunc func(context.Context, string) (*models.Deployment, error)
		statusFunc = provider.GetDeploymentStatus
		require.NotNil(t, statusFunc)
	})

	t.Run("ListDeployments_Signature", func(t *testing.T) {
		var listFunc func(context.Context, int, string) ([]*models.Deployment, error)
		listFunc = provider.ListDeployments
		require.NotNil(t, listFunc)
	})

	t.Run("UpdateDeployment_Signature", func(t *testing.T) {
		var updateFunc func(context.Context, string, int32) (*models.Deployment, error)
		updateFunc = provider.UpdateDeployment
		require.NotNil(t, updateFunc)
	})

	t.Run("DeleteDeployment_Signature", func(t *testing.T) {
		var deleteFunc func(context.Context, string) error
		deleteFunc = provider.DeleteDeployment
		require.NotNil(t, deleteFunc)
	})
}
