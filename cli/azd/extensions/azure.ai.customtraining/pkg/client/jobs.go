// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"azure.ai.customtraining/pkg/models"
)

// ListJobs lists all jobs in the project.
// GET .../jobs
func (c *Client) ListJobs(ctx context.Context) (*models.PagedResponse, error) {
	resp, err := c.doDataPlane(ctx, http.MethodGet, "jobs", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.PagedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// GetJob retrieves a specific job by ID.
// GET .../jobs/{id}
func (c *Client) GetJob(ctx context.Context, id string) (*models.JobResource, error) {
	resp, err := c.doDataPlane(ctx, http.MethodGet, fmt.Sprintf("jobs/%s", id), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get job: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.JobResource
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// CreateOrUpdateJob creates or updates a job.
// PUT .../jobs/{id}
func (c *Client) CreateOrUpdateJob(ctx context.Context, id string, job *models.JobResource) (*models.JobResource, error) {
	resp, err := c.doDataPlane(ctx, http.MethodPut, fmt.Sprintf("jobs/%s", id), job)
	if err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, c.HandleError(resp)
	}

	var result models.JobResource
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// CancelJob cancels a running job.
// POST .../jobs/{id}/cancel
func (c *Client) CancelJob(ctx context.Context, id string) error {
	resp, err := c.doDataPlane(ctx, http.MethodPost, fmt.Sprintf("jobs/%s/cancel", id), nil)
	if err != nil {
		return fmt.Errorf("failed to cancel job: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return c.HandleError(resp)
	}

	return nil
}

// DeleteJob deletes a job.
// DELETE .../jobs/{id}
func (c *Client) DeleteJob(ctx context.Context, id string) error {
	resp, err := c.doDataPlane(ctx, http.MethodDelete, fmt.Sprintf("jobs/%s", id), nil)
	if err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.HandleError(resp)
	}

	return nil
}
