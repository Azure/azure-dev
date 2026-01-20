// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeploymentStatus_Constants(t *testing.T) {
	tests := []struct {
		name     string
		status   DeploymentStatus
		expected string
	}{
		{"DeploymentPending", DeploymentPending, "pending"},
		{"DeploymentActive", DeploymentActive, "active"},
		{"DeploymentUpdating", DeploymentUpdating, "updating"},
		{"DeploymentFailed", DeploymentFailed, "failed"},
		{"DeploymentDeleting", DeploymentDeleting, "deleting"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, string(tt.status))
		})
	}
}

func TestDeployment_Struct(t *testing.T) {
	t.Run("BasicDeployment", func(t *testing.T) {
		deployment := &Deployment{
			ID:             "deploy-123",
			VendorID:       "vendor-abc",
			Name:           "my-deployment",
			Status:         DeploymentActive,
			FineTunedModel: "ft:gpt-4o-mini:my-org::abc123",
			BaseModel:      "gpt-4o-mini",
			Endpoint:       "https://api.example.com/v1",
		}

		require.Equal(t, "deploy-123", deployment.ID)
		require.Equal(t, "vendor-abc", deployment.VendorID)
		require.Equal(t, "my-deployment", deployment.Name)
		require.Equal(t, DeploymentActive, deployment.Status)
		require.Equal(t, "ft:gpt-4o-mini:my-org::abc123", deployment.FineTunedModel)
		require.Equal(t, "gpt-4o-mini", deployment.BaseModel)
		require.Equal(t, "https://api.example.com/v1", deployment.Endpoint)
	})

	t.Run("DeploymentWithMetadata", func(t *testing.T) {
		deployment := &Deployment{
			ID:     "deploy-456",
			Name:   "test-deployment",
			Status: DeploymentPending,
			VendorMetadata: map[string]interface{}{
				"region":   "eastus",
				"capacity": 10,
			},
		}

		require.NotNil(t, deployment.VendorMetadata)
		require.Equal(t, "eastus", deployment.VendorMetadata["region"])
		require.Equal(t, 10, deployment.VendorMetadata["capacity"])
	})

	t.Run("DeploymentWithErrorDetails", func(t *testing.T) {
		deployment := &Deployment{
			ID:     "deploy-789",
			Name:   "failed-deployment",
			Status: DeploymentFailed,
			ErrorDetails: &ErrorDetail{
				Code:    ErrorCodeOperationFailed,
				Message: "Deployment failed due to insufficient quota",
			},
		}

		require.NotNil(t, deployment.ErrorDetails)
		require.Equal(t, ErrorCodeOperationFailed, deployment.ErrorDetails.Code)
		require.Contains(t, deployment.ErrorDetails.Message, "insufficient quota")
	})
}

func TestDeploymentRequest_Struct(t *testing.T) {
	t.Run("CompleteRequest", func(t *testing.T) {
		request := &DeploymentRequest{
			ModelName:         "ft:gpt-4o-mini:my-org::abc123",
			DeploymentName:    "my-deployment",
			ModelFormat:       "OpenAI",
			SKU:               "Standard",
			Version:           "1",
			Capacity:          10,
			SubscriptionID:    "sub-123",
			ResourceGroup:     "my-rg",
			AccountName:       "my-account",
			TenantID:          "tenant-123",
			WaitForCompletion: true,
		}

		require.Equal(t, "ft:gpt-4o-mini:my-org::abc123", request.ModelName)
		require.Equal(t, "my-deployment", request.DeploymentName)
		require.Equal(t, "OpenAI", request.ModelFormat)
		require.Equal(t, "Standard", request.SKU)
		require.Equal(t, "1", request.Version)
		require.Equal(t, int32(10), request.Capacity)
		require.Equal(t, "sub-123", request.SubscriptionID)
		require.Equal(t, "my-rg", request.ResourceGroup)
		require.Equal(t, "my-account", request.AccountName)
		require.Equal(t, "tenant-123", request.TenantID)
		require.True(t, request.WaitForCompletion)
	})

	t.Run("MinimalRequest", func(t *testing.T) {
		request := &DeploymentRequest{
			ModelName:      "gpt-4o-mini",
			DeploymentName: "test-deploy",
		}

		require.Equal(t, "gpt-4o-mini", request.ModelName)
		require.Equal(t, "test-deploy", request.DeploymentName)
		require.False(t, request.WaitForCompletion)
	})
}

func TestDeploymentConfig_Struct(t *testing.T) {
	t.Run("CompleteConfig", func(t *testing.T) {
		config := &DeploymentConfig{
			JobID:             "job-123",
			DeploymentName:    "my-deployment",
			ModelFormat:       "OpenAI",
			SKU:               "Standard",
			Version:           "1",
			Capacity:          5,
			SubscriptionID:    "sub-456",
			ResourceGroup:     "my-rg",
			AccountName:       "my-account",
			TenantID:          "tenant-456",
			WaitForCompletion: false,
		}

		require.Equal(t, "job-123", config.JobID)
		require.Equal(t, "my-deployment", config.DeploymentName)
		require.Equal(t, int32(5), config.Capacity)
		require.False(t, config.WaitForCompletion)
	})
}

func TestDeployModelResult_Struct(t *testing.T) {
	t.Run("SuccessfulResult", func(t *testing.T) {
		result := &DeployModelResult{
			Deployment: Deployment{
				ID:             "deploy-123",
				Name:           "my-deployment",
				Status:         DeploymentActive,
				FineTunedModel: "ft:gpt-4o-mini:my-org::abc123",
			},
			Status:  "succeeded",
			Message: "Model deployed successfully",
		}

		require.Equal(t, "deploy-123", result.Deployment.ID)
		require.Equal(t, "succeeded", result.Status)
		require.Contains(t, result.Message, "successfully")
	})

	t.Run("InProgressResult", func(t *testing.T) {
		result := &DeployModelResult{
			Deployment: Deployment{
				Name:   "pending-deployment",
				Status: DeploymentPending,
			},
			Status:  "in_progress",
			Message: "Deployment initiated",
		}

		require.Equal(t, "in_progress", result.Status)
		require.Equal(t, DeploymentPending, result.Deployment.Status)
	})
}

func TestBaseModel_Struct(t *testing.T) {
	t.Run("ActiveModel", func(t *testing.T) {
		model := &BaseModel{
			ID:          "gpt-4o-mini",
			Name:        "GPT-4o Mini",
			Description: "A smaller, faster version of GPT-4",
			Deprecated:  false,
		}

		require.Equal(t, "gpt-4o-mini", model.ID)
		require.Equal(t, "GPT-4o Mini", model.Name)
		require.False(t, model.Deprecated)
	})

	t.Run("DeprecatedModel", func(t *testing.T) {
		model := &BaseModel{
			ID:          "gpt-3.5-turbo-0301",
			Name:        "GPT-3.5 Turbo (0301)",
			Description: "Legacy version of GPT-3.5",
			Deprecated:  true,
		}

		require.Equal(t, "gpt-3.5-turbo-0301", model.ID)
		require.True(t, model.Deprecated)
	})
}
