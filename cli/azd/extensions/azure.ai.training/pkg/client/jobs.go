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
	"time"

	"azure.ai.training/pkg/models"
)

// Polling defaults for DeleteJob. Foundry deletes are usually seconds; the
// cap protects against a runaway poll loop if the backend gets wedged.
const (
	defaultDeletePollInterval = 3 * time.Second
	maxDeletePollDuration     = 5 * time.Minute
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

// DeleteJobOptions configures the behavior of DeleteJob.
type DeleteJobOptions struct {
	// NoWait, when true, peeks the operation-result URL exactly once and
	// returns immediately — even if the deletion is still running. The
	// single peek is preserved (vs. skipping it entirely) so callers can
	// distinguish a fast synchronous completion from an in-progress
	// deletion that was merely accepted.
	//
	// When false (default), DeleteJob polls the operation-result URL until
	// the deletion reaches a terminal state, honoring server Retry-After,
	// or until the maxDeletePollDuration cap elapses (in which case
	// DeleteJobInProgress is returned without an error).
	NoWait bool
}

// DeleteJob deletes a job.
//
//	DELETE .../jobs/{id}
//
// Per the Foundry contract the initial DELETE returns:
//   - 202 Accepted: deletion started; the Location header points to an
//     operation-result URL. By default we poll that URL until the deletion
//     reaches a terminal state (or maxDeletePollDuration elapses); pass
//     opts.NoWait = true to peek exactly once and return immediately.
//   - 204 No Content: job was not found (or already deleted) — idempotent success.
//   - 200 OK: synchronous ack with no Location to follow.
//   - 4xx/5xx: surfaced as an error.
//
// On the operation-result poll:
//   - 200 OK: deletion completed.
//   - 202 Accepted: still in progress (poll again, or return InProgress in NoWait mode).
//   - 4xx/5xx: surfaced as an error (e.g. 404 means the operation URL is no
//     longer valid).
func (c *Client) DeleteJob(
	ctx context.Context, id string, opts *DeleteJobOptions,
) (*DeleteJobResult, error) {
	if opts == nil {
		opts = &DeleteJobOptions{}
	}

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
		return c.pollDeleteOperation(ctx, location, opts.NoWait)
	default:
		return nil, c.HandleError(resp)
	}
}

// pollDeleteOperation issues authenticated GETs against the operation-result
// URL returned by the initial DELETE.
//
// When noWait is true, only the first peek is performed: a 202 maps to
// DeleteJobInProgress and we return immediately. When noWait is false, 202
// responses cause the loop to sleep (honoring Retry-After, defaulting to
// defaultDeletePollInterval) and retry until the deletion completes or the
// maxDeletePollDuration deadline is hit; on timeout we still return a
// successful DeleteJobInProgress so the CLI can tell the user to check
// status manually rather than failing the command.
func (c *Client) pollDeleteOperation(
	ctx context.Context, locationURL string, noWait bool,
) (*DeleteJobResult, error) {
	deadline := time.Now().Add(maxDeletePollDuration)
	for {
		opResp, err := c.getAbsoluteDataPlane(ctx, locationURL)
		if err != nil {
			return nil, fmt.Errorf("failed to poll delete operation: %w", err)
		}

		switch opResp.StatusCode {
		case http.StatusOK:
			opResp.Body.Close()
			return &DeleteJobResult{Status: DeleteJobCompleted}, nil

		case http.StatusAccepted:
			retryAfter := parseRetryAfter(opResp.Header.Get("Retry-After"))
			if retryAfter <= 0 {
				retryAfter = defaultDeletePollInterval
			}
			opResp.Body.Close()
			if noWait {
				return &DeleteJobResult{Status: DeleteJobInProgress}, nil
			}
			if !time.Now().Add(retryAfter).Before(deadline) {
				// Next sleep would push us past the cap — treat as still-in-progress
				// rather than blocking longer or failing the command.
				return &DeleteJobResult{Status: DeleteJobInProgress}, nil
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryAfter):
			}

		default:
			err := c.HandleError(opResp)
			opResp.Body.Close()
			return nil, err
		}
	}
}

// parseRetryAfter lives in client.go and is reused here.
