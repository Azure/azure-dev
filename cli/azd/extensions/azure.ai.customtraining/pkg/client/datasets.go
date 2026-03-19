// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"azure.ai.customtraining/pkg/models"
)

// StartPendingUpload initiates a pending upload for a dataset version.
// POST .../datasets/{name}/versions/{version}/startPendingUpload
func (c *Client) StartPendingUpload(
	ctx context.Context, datasetName, version string,
) (*models.PendingUploadResponse, error) {
	path := fmt.Sprintf("datasets/%s/versions/%s/startPendingUpload",
		url.PathEscape(datasetName), url.PathEscape(version))

	reqBody := &models.PendingUploadRequest{
		PendingUploadType: "BlobReference",
	}

	resp, err := c.doDataPlane(ctx, http.MethodPost, path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to start pending upload for dataset %s: %w", datasetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.PendingUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode pending upload response: %w", err)
	}

	return &result, nil
}

// CreateOrUpdateDatasetVersion creates or updates a dataset version.
// PATCH .../datasets/{name}/versions/{version}
func (c *Client) CreateOrUpdateDatasetVersion(
	ctx context.Context, datasetName, version string, dataset *models.DatasetVersion,
) (*models.DatasetVersion, error) {
	path := fmt.Sprintf("datasets/%s/versions/%s",
		url.PathEscape(datasetName), url.PathEscape(version))

	resp, err := c.doDataPlane(ctx, http.MethodPatch, path, dataset)
	if err != nil {
		return nil, fmt.Errorf("failed to create/update dataset %s: %w", datasetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, c.HandleError(resp)
	}

	var result models.DatasetVersion
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode dataset response: %w", err)
	}

	return &result, nil
}

// GetDatasetVersion retrieves a dataset version.
// GET .../datasets/{name}/versions/{version}
//
// Returns (nil, nil) if the dataset version does not exist (HTTP 404).
// This makes it easy for callers to check existence without error-type inspection.
func (c *Client) GetDatasetVersion(
	ctx context.Context, datasetName, version string,
) (*models.DatasetVersion, error) {
	path := fmt.Sprintf("datasets/%s/versions/%s",
		url.PathEscape(datasetName), url.PathEscape(version))

	resp, err := c.doDataPlane(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get dataset %s: %w", datasetName, err)
	}
	defer resp.Body.Close()

	// 404 means the dataset version doesn't exist — return nil without error
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.DatasetVersion
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode dataset response: %w", err)
	}

	return &result, nil
}

// DeleteDatasetVersion deletes a dataset version.
// DELETE .../datasets/{name}/versions/{version}
func (c *Client) DeleteDatasetVersion(
	ctx context.Context, datasetName, version string,
) error {
	path := fmt.Sprintf("datasets/%s/versions/%s",
		url.PathEscape(datasetName), url.PathEscape(version))

	resp, err := c.doDataPlane(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("failed to delete dataset %s: %w", datasetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.HandleError(resp)
	}

	return nil
}
