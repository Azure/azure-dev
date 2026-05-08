// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"azure.ai.customtraining/pkg/models"
)

// GetRunHistory retrieves run history details for a specific job.
// GET .../history/runs/{runId}
func (c *Client) GetRunHistory(ctx context.Context, runID string) (*models.RunHistory, error) {
	resp, err := c.doDataPlane(ctx, http.MethodGet, fmt.Sprintf("history/runs/%s", runID), nil)
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
// This calls the AML history service directly using the tracking endpoint from the job response.
// GET https://{region}.api.azureml.ms/history/v1.0/{workspace}/runs/{runId}/details
func (c *Client) GetRunHistoryDetails(ctx context.Context, trackingEndpoint string, runID string) (*models.RunHistoryDetails, error) {
	baseURL, workspacePath, err := parseTrackingEndpoint(trackingEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tracking endpoint: %w", err)
	}

	reqURL := fmt.Sprintf("%s/history/v1.0%s/runs/%s/details", baseURL, workspacePath, url.PathEscape(runID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(ctx, req, DataPlaneScope); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	if c.debugBody {
		fmt.Printf("[DEBUG] GET %s\n", reqURL)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
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

// parseTrackingEndpoint extracts the base URL and workspace path from a tracking endpoint.
// Input format:  azureml://{region}.api.azureml.ms/mlflow/v1.0/{workspace-path}?...
// Returns:       https://{region}.api.azureml.ms, /{workspace-path}
func parseTrackingEndpoint(trackingEndpoint string) (string, string, error) {
	// Remove the azureml:// prefix
	endpoint := strings.TrimPrefix(trackingEndpoint, "azureml://")
	// Remove any trailing query params
	if idx := strings.Index(endpoint, "?"); idx != -1 {
		endpoint = endpoint[:idx]
	}

	// Parse: {host}/mlflow/v1.0/{subscription-path}
	parsed, err := url.Parse("https://" + endpoint)
	if err != nil {
		return "", "", fmt.Errorf("invalid tracking endpoint: %w", err)
	}

	host := parsed.Host
	if host == "" {
		return "", "", fmt.Errorf("invalid tracking endpoint: missing host")
	}

	// Extract workspace path: everything after /mlflow/v1.0/ or /mlflow/v2.0/
	path := parsed.Path
	for _, prefix := range []string{"/mlflow/v1.0", "/mlflow/v2.0"} {
		if strings.HasPrefix(path, prefix) {
			workspacePath := strings.TrimPrefix(path, prefix)
			return "https://" + host, workspacePath, nil
		}
	}

	return "", "", fmt.Errorf("invalid tracking endpoint: expected mlflow path prefix in %q", path)
}
