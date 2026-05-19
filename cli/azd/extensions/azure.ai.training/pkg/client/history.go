// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"azure.ai.training/pkg/models"
)

// GetRunHistory retrieves run history details for a specific job.
//
//	GET .../jobs/{name}/history/runs/{runId}
//
// For Jobs, runId matches the job name.
// Returns nil with no error when the run does not exist (404).
func (c *Client) GetRunHistory(ctx context.Context, jobName string) (*models.RunHistory, error) {
	path := fmt.Sprintf(
		"jobs/%s/history/runs/%s",
		url.PathEscape(jobName), url.PathEscape(jobName),
	)

	resp, err := c.doDataPlane(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get run history: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.RunHistory
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode run history response: %w", err)
	}

	return &result, nil
}

// GetRunHistoryDetails retrieves detailed run information including log file SAS URIs.
//
//	GET .../jobs/{name}/history/runs/{runId}/details
//
// For Jobs, runId matches the job name.
// Returns nil with no error when the run does not exist (404).
func (c *Client) GetRunHistoryDetails(ctx context.Context, jobName string) (*models.RunHistoryDetails, error) {
	path := fmt.Sprintf(
		"jobs/%s/history/runs/%s/details",
		url.PathEscape(jobName), url.PathEscape(jobName),
	)

	resp, err := c.doDataPlane(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get run history details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.RunHistoryDetails
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode run history details response: %w", err)
	}

	return &result, nil
}
