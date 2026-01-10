// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package services

import (
	"context"

	"azure.ai.finetune/internal/providers"
	"azure.ai.finetune/pkg/models"
)

// Ensure deploymentServiceImpl implements DeploymentService interface
var _ DeploymentService = (*deploymentServiceImpl)(nil)

// deploymentServiceImpl implements the DeploymentService interface
type deploymentServiceImpl struct {
	provider   providers.ModelDeploymentProvider
	stateStore StateStore
}

// NewDeploymentService creates a new instance of DeploymentService
func NewDeploymentService(provider providers.ModelDeploymentProvider, stateStore StateStore) DeploymentService {
	return &deploymentServiceImpl{
		provider:   provider,
		stateStore: stateStore,
	}
}

// DeployModel deploys a fine-tuned or base model with validation
func (s *deploymentServiceImpl) DeployModel(ctx context.Context, req *models.DeploymentRequest) (*models.Deployment, error) {
	// TODO: Implement
	// 1. Validate request (deployment name format, SKU valid, capacity valid, etc.)
	// 2. Call provider.DeployModel()
	// 3. Transform any errors to standardized ErrorDetail
	// 4. Persist deployment to state store
	// 5. Return deployment
	return nil, nil
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
