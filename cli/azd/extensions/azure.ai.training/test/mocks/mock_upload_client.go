// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mocks

import (
	"context"

	"azure.ai.training/pkg/models"

	"github.com/stretchr/testify/mock"
)

// UploadClient mocks the Foundry dataset operations used by the upload service.
type UploadClient struct {
	mock.Mock
}

// GetDatasetVersion mocks retrieval of a Foundry dataset version.
func (m *UploadClient) GetDatasetVersion(
	ctx context.Context,
	datasetName string,
	version string,
) (*models.DatasetVersion, error) {
	args := m.Called(ctx, datasetName, version)
	var result *models.DatasetVersion
	if value := args.Get(0); value != nil {
		result = value.(*models.DatasetVersion)
	}
	return result, args.Error(1)
}

// DeleteDatasetVersion mocks deletion of a Foundry dataset version.
func (m *UploadClient) DeleteDatasetVersion(
	ctx context.Context,
	datasetName string,
	version string,
) error {
	return m.Called(ctx, datasetName, version).Error(0)
}

// StartPendingUpload mocks creation of a pending Foundry dataset upload.
func (m *UploadClient) StartPendingUpload(
	ctx context.Context,
	datasetName string,
	version string,
	connectionName string,
) (*models.PendingUploadResponse, error) {
	args := m.Called(ctx, datasetName, version, connectionName)
	var result *models.PendingUploadResponse
	if value := args.Get(0); value != nil {
		result = value.(*models.PendingUploadResponse)
	}
	return result, args.Error(1)
}

// CreateOrUpdateDatasetVersion mocks confirmation of a Foundry dataset upload.
func (m *UploadClient) CreateOrUpdateDatasetVersion(
	ctx context.Context,
	datasetName string,
	version string,
	dataset *models.DatasetVersion,
) (*models.DatasetVersion, error) {
	args := m.Called(ctx, datasetName, version, dataset)
	var result *models.DatasetVersion
	if value := args.Get(0); value != nil {
		result = value.(*models.DatasetVersion)
	}
	return result, args.Error(1)
}
