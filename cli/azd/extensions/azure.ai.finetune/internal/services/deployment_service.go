// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package services

import (
	"context"
	"fmt"

	"azure.ai.finetune/internal/providers"
	"azure.ai.finetune/pkg/models"
)

// Ensure deploymentServiceImpl implements DeploymentService interface
var _ DeploymentService = (*deploymentServiceImpl)(nil)

// deploymentServiceImpl implements the DeploymentService interface
type deploymentServiceImpl struct {
	provider   providers.ModelDeploymentProvider
	ftProvider providers.FineTuningProvider
	stateStore StateStore
}

// NewDeploymentService creates a new instance of DeploymentService
func NewDeploymentService(provider providers.ModelDeploymentProvider, ftProvider providers.FineTuningProvider, stateStore StateStore) DeploymentService {
	return &deploymentServiceImpl{
		provider:   provider,
		ftProvider: ftProvider,
		stateStore: stateStore,
	}
}

// DeployModel deploys a fine-tuned or base model with validation
func (s *deploymentServiceImpl) DeployModel(ctx context.Context, req *models.DeploymentConfig) (*models.DeployModelResult, error) {
	if req == nil {
		return nil, fmt.Errorf("deployment request cannot be nil")
	}

	if req.JobID == "" || req.DeploymentName == "" {
		return nil, fmt.Errorf("JobID and DeploymentName must be provided")
	}

	// Get job details and extract fine-tuned model name
	fmt.Println("\nRetrieving fine-tuning job details...")
	jobDetails, err := s.ftProvider.GetFineTuningJobDetails(ctx, req.JobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get fine-tuning job details: %w", err)
	}

	if jobDetails == nil || jobDetails.FineTunedModel == "" {
		return nil, fmt.Errorf("fine-tuned model not found for job ID: %s", req.JobID)
	}

	// Create new request with fine-tuned model name
	internalReq := &models.DeploymentRequest{
		DeploymentName:    req.DeploymentName,
		ModelName:         jobDetails.FineTunedModel,
		SKU:               req.SKU,
		Capacity:          req.Capacity,
		SubscriptionID:    req.SubscriptionID,
		ResourceGroup:     req.ResourceGroup,
		AccountName:       req.AccountName,
		TenantID:          req.TenantID,
		Version:           req.Version,
		ModelFormat:       req.ModelFormat,
		WaitForCompletion: req.WaitForCompletion,
	}

	deployedModel, err := s.provider.DeployModel(ctx, internalReq)
	if err != nil {
		return nil, fmt.Errorf("failed to deploy model: %w", err)
	}

	return deployedModel, nil
}

// GetDeploymentStatus retrieves the current status of a deployment
func (s *deploymentServiceImpl) GetDeploymentStatus(ctx context.Context, deploymentID string) (*models.Deployment, error) {
	// TODO: Implement
	return nil, nil
}

// ListDeployments lists all deployments for the user
func (s *deploymentServiceImpl) ListDeployments(ctx context.Context, limit int, after string) ([]*models.Deployment, error) {
	// TODO: Implement
	return nil, nil
}

// UpdateDeployment updates deployment configuration (e.g., capacity)
func (s *deploymentServiceImpl) UpdateDeployment(ctx context.Context, deploymentID string, capacity int32) (*models.Deployment, error) {
	// TODO: Implement
	return nil, nil
}

// DeleteDeployment deletes a deployment with proper validation
func (s *deploymentServiceImpl) DeleteDeployment(ctx context.Context, deploymentID string) error {
	// TODO: Implement
	return nil
}

// WaitForDeployment waits for a deployment to become active
func (s *deploymentServiceImpl) WaitForDeployment(ctx context.Context, deploymentID string, timeoutSeconds int) (*models.Deployment, error) {
	// TODO: Implement
	return nil, nil
}
