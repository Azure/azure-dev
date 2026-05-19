// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"azure.ai.training/pkg/models"
)

// ListJobsOptions contains optional parameters for listing jobs.
type ListJobsOptions struct {
	SkipToken       string
	Tag             string
	Properties      string
	IncludeArchived bool
}

// ListJobs lists all jobs in the project.
// GET .../jobs
func (c *Client) ListJobs(ctx context.Context, opts *ListJobsOptions) (*models.PagedResponse, error) {
	var queryParams []string
	if opts != nil && opts.SkipToken != "" {
		queryParams = append(queryParams, "$skipToken", opts.SkipToken)
	}
	if opts != nil && opts.Tag != "" {
		queryParams = append(queryParams, "tag", opts.Tag)
	}
	if opts != nil && opts.Properties != "" {
		queryParams = append(queryParams, "properties", opts.Properties)
	}
	if opts != nil && opts.IncludeArchived {
		queryParams = append(queryParams, "listViewType", "All")
	}

	resp, err := c.doDataPlane(ctx, http.MethodGet, "jobs", nil, queryParams...)
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
	resp, err := c.doDataPlane(ctx, http.MethodGet, fmt.Sprintf("jobs/%s", url.PathEscape(id)), nil)
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
	resp, err := c.doDataPlane(ctx, http.MethodPut, fmt.Sprintf("jobs/%s", url.PathEscape(id)), job)
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
	resp, err := c.doDataPlane(ctx, http.MethodPost, fmt.Sprintf("jobs/%s/cancel", url.PathEscape(id)), nil)
	if err != nil {
		return fmt.Errorf("failed to cancel job: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return c.HandleError(resp)
	}

	return nil
}

// DeleteJobStatus describes the outcome of a DeleteJob call.
type DeleteJobStatus int

const (
	// DeleteJobCompleted: deletion finished (initial 200 OK, or 202 + operation poll 200).
	DeleteJobCompleted DeleteJobStatus = iota
	// DeleteJobNotFound: initial 204 No Content — job not found / already deleted (idempotent success).
	DeleteJobNotFound
	// DeleteJobInProgress: deletion is still running (operation poll returned 202).
	DeleteJobInProgress
	// DeleteJobAccepted: initial 202 with no usable Location header — deletion accepted but unverified.
	DeleteJobAccepted
)

// DeleteJobResult is returned by DeleteJob.
type DeleteJobResult struct {
	Status DeleteJobStatus
}

// DeleteJob deletes a job.
//
//	DELETE .../jobs/{id}
//
// Per the Foundry contract the initial DELETE returns:
//   - 202 Accepted: deletion started; the Location header points to an
//     operation-result URL. We make a single follow-up GET on that URL (no
//     polling loop) so we can surface an accurate outcome to the caller.
//   - 204 No Content: job was not found (or already deleted) — idempotent success.
//   - 200 OK: synchronous ack with no Location to follow.
//   - 4xx/5xx: surfaced as an error.
//
// On the operation-result follow-up:
//   - 200 OK: deletion completed.
//   - 202 Accepted: still in progress (we do not poll).
//   - 4xx: typically means the job was not in a terminal state and cannot be
//     deleted; surfaced as an error.
func (c *Client) DeleteJob(ctx context.Context, id string) (*DeleteJobResult, error) {
	resp, err := c.doDataPlane(ctx, http.MethodDelete, fmt.Sprintf("jobs/%s", url.PathEscape(id)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to delete job: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return &DeleteJobResult{Status: DeleteJobCompleted}, nil
	case http.StatusNoContent:
		return &DeleteJobResult{Status: DeleteJobNotFound}, nil
	case http.StatusAccepted:
		// Drain initial body so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		location := resp.Header.Get("Location")
		if location == "" {
			return &DeleteJobResult{Status: DeleteJobAccepted}, nil
		}
		return c.pollDeleteOperationOnce(ctx, location)
	default:
		return nil, c.HandleError(resp)
	}
}

// pollDeleteOperationOnce issues a single authenticated GET against the
// operation-result URL returned in the DELETE Location header and maps the
// response to a DeleteJobResult. It does NOT loop.
func (c *Client) pollDeleteOperationOnce(ctx context.Context, locationURL string) (*DeleteJobResult, error) {
	opResp, err := c.getAbsoluteDataPlane(ctx, locationURL)
	if err != nil {
		return nil, fmt.Errorf("failed to poll delete operation: %w", err)
	}
	defer opResp.Body.Close()

	switch opResp.StatusCode {
	case http.StatusOK:
		return &DeleteJobResult{Status: DeleteJobCompleted}, nil
	case http.StatusAccepted:
		return &DeleteJobResult{Status: DeleteJobInProgress}, nil
	default:
		return nil, c.HandleError(opResp)
	}
}
