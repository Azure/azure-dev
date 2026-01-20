// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"context"
	"testing"

	"azure.ai.finetune/pkg/models"
	"github.com/stretchr/testify/require"
)

func TestAzureProvider_DeployModel_Validation(t *testing.T) {
	// Create a provider with nil clientFactory to test validation only
	// (actual deployment would fail, but validation happens first)
	provider := NewAzureProvider(nil)

	tests := []struct {
		name          string
		config        *models.DeploymentRequest
		errorContains string
	}{
		{
			name: "MissingModelName",
			config: &models.DeploymentRequest{
				DeploymentName: "my-deployment",
				SubscriptionID: "sub-123",
				ResourceGroup:  "my-rg",
				AccountName:    "my-account",
				TenantID:       "tenant-123",
			},
			errorContains: "could not find model name",
		},
		{
			name: "MissingDeploymentName",
			config: &models.DeploymentRequest{
				ModelName:      "gpt-4o-mini",
				SubscriptionID: "sub-123",
				ResourceGroup:  "my-rg",
				AccountName:    "my-account",
				TenantID:       "tenant-123",
			},
			errorContains: "deployment name is required",
		},
		{
			name: "MissingSubscriptionID",
			config: &models.DeploymentRequest{
				ModelName:      "gpt-4o-mini",
				DeploymentName: "my-deployment",
				ResourceGroup:  "my-rg",
				AccountName:    "my-account",
				TenantID:       "tenant-123",
			},
			errorContains: "subscription ID is required",
		},
		{
			name: "MissingResourceGroup",
			config: &models.DeploymentRequest{
				ModelName:      "gpt-4o-mini",
				DeploymentName: "my-deployment",
				SubscriptionID: "sub-123",
				AccountName:    "my-account",
				TenantID:       "tenant-123",
			},
			errorContains: "resource group is required",
		},
		{
			name: "MissingAccountName",
			config: &models.DeploymentRequest{
				ModelName:      "gpt-4o-mini",
				DeploymentName: "my-deployment",
				SubscriptionID: "sub-123",
				ResourceGroup:  "my-rg",
				TenantID:       "tenant-123",
			},
			errorContains: "account name is required",
		},
		{
			name: "MissingTenantID",
			config: &models.DeploymentRequest{
				ModelName:      "gpt-4o-mini",
				DeploymentName: "my-deployment",
				SubscriptionID: "sub-123",
				ResourceGroup:  "my-rg",
				AccountName:    "my-account",
			},
			errorContains: "tenant ID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := provider.DeployModel(context.Background(), tt.config)

			require.Error(t, err)
			require.Nil(t, result)
			require.Contains(t, err.Error(), tt.errorContains)
		})
	}
}

func TestAzureProvider_DeployModel_ValidationOrder(t *testing.T) {
	provider := NewAzureProvider(nil)

	// Test that validation happens in a specific order
	// Empty config should fail on model name first
	emptyConfig := &models.DeploymentRequest{}

	result, err := provider.DeployModel(context.Background(), emptyConfig)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "model name")
}

func TestNewAzureProvider(t *testing.T) {
	// Test that provider can be created with nil client factory
	provider := NewAzureProvider(nil)

	require.NotNil(t, provider)
	require.Nil(t, provider.clientFactory)
}

func TestAzureProvider_NotImplementedMethods(t *testing.T) {
	provider := NewAzureProvider(nil)
	ctx := context.Background()

	t.Run("CreateFineTuningJob", func(t *testing.T) {
		job, err := provider.CreateFineTuningJob(ctx, &models.CreateFineTuningRequest{})
		// Currently returns nil, nil (not implemented)
		require.NoError(t, err)
		require.Nil(t, job)
	})

	t.Run("GetFineTuningStatus", func(t *testing.T) {
		job, err := provider.GetFineTuningStatus(ctx, "job-123")
		require.NoError(t, err)
		require.Nil(t, job)
	})

	t.Run("ListFineTuningJobs", func(t *testing.T) {
		jobs, err := provider.ListFineTuningJobs(ctx, 10, "")
		require.NoError(t, err)
		require.Nil(t, jobs)
	})

	t.Run("GetFineTuningJobDetails", func(t *testing.T) {
		details, err := provider.GetFineTuningJobDetails(ctx, "job-123")
		require.NoError(t, err)
		require.Nil(t, details)
	})

	t.Run("PauseJob", func(t *testing.T) {
		job, err := provider.PauseJob(ctx, "job-123")
		require.NoError(t, err)
		require.Nil(t, job)
	})

	t.Run("ResumeJob", func(t *testing.T) {
		job, err := provider.ResumeJob(ctx, "job-123")
		require.NoError(t, err)
		require.Nil(t, job)
	})

	t.Run("CancelJob", func(t *testing.T) {
		job, err := provider.CancelJob(ctx, "job-123")
		require.NoError(t, err)
		require.Nil(t, job)
	})

	t.Run("UploadFile", func(t *testing.T) {
		fileID, err := provider.UploadFile(ctx, "/path/to/file.jsonl")
		require.NoError(t, err)
		require.Empty(t, fileID)
	})

	t.Run("GetUploadedFile", func(t *testing.T) {
		file, err := provider.GetUploadedFile(ctx, "file-123")
		require.NoError(t, err)
		require.Nil(t, file)
	})

	t.Run("GetDeploymentStatus", func(t *testing.T) {
		deployment, err := provider.GetDeploymentStatus(ctx, "deploy-123")
		require.NoError(t, err)
		require.Nil(t, deployment)
	})

	t.Run("ListDeployments", func(t *testing.T) {
		deployments, err := provider.ListDeployments(ctx, 10, "")
		require.NoError(t, err)
		require.Nil(t, deployments)
	})

	t.Run("UpdateDeployment", func(t *testing.T) {
		deployment, err := provider.UpdateDeployment(ctx, "deploy-123", 10)
		require.NoError(t, err)
		require.Nil(t, deployment)
	})
}
