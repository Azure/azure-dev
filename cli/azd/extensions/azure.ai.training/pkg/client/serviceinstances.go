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
	"os"
	"strconv"

	"azure.ai.training/pkg/models"
)

// GetServiceInstance retrieves service instance details for a specific node of a run.
// Calls the AML history service:
//
//	GET https://{region}.api.azureml.ms/history/v1.0/{workspace}/runs/{runId}/serviceinstances/{nodeIndex}
//
// Returns nil with no error when the node does not exist (404).
func (c *Client) GetServiceInstance(
	ctx context.Context,
	trackingEndpoint string,
	runID string,
	nodeIndex int,
) (*models.ServiceInstance, error) {
	baseURL, workspacePath, err := parseTrackingEndpoint(trackingEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tracking endpoint: %w", err)
	}

	reqURL := fmt.Sprintf(
		"%s/history/v1.0%s/runs/%s/serviceinstances/%s",
		baseURL,
		workspacePath,
		url.PathEscape(runID),
		strconv.Itoa(nodeIndex),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(ctx, req, DataPlaneScope); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	if c.debugBody {
		fmt.Fprintf(os.Stderr, "[DEBUG] GET %s\n", reqURL)
	}

	resp, err := c.do(req, nil)
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

	var result models.ServiceInstance
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode service instance response: %w", err)
	}

	return &result, nil
}

// GetServiceInstanceRaw is like GetServiceInstance but returns the raw JSON
// response body so the caller can pass it through to output without losing
// any fields (e.g. nullable values, fields not modeled in Go structs).
// Returns nil with no error when the node does not exist (404).
func (c *Client) GetServiceInstanceRaw(
	ctx context.Context,
	trackingEndpoint string,
	runID string,
	nodeIndex int,
) (json.RawMessage, error) {
	baseURL, workspacePath, err := parseTrackingEndpoint(trackingEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tracking endpoint: %w", err)
	}

	reqURL := fmt.Sprintf(
		"%s/history/v1.0%s/runs/%s/serviceinstances/%s",
		baseURL,
		workspacePath,
		url.PathEscape(runID),
		strconv.Itoa(nodeIndex),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(ctx, req, DataPlaneScope); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	if c.debugBody {
		fmt.Fprintf(os.Stderr, "[DEBUG] GET %s\n", reqURL)
	}

	resp, err := c.do(req, nil)
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

	// Cap the response body to guard against a misbehaving server streaming an
	// unbounded payload. 4 MiB easily covers any realistic service-instance
	// listing while keeping memory use bounded.
	const maxServiceInstanceBodyBytes = 4 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxServiceInstanceBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read service instance response: %w", err)
	}

	return json.RawMessage(body), nil
}

// GetARMToken returns a bearer token scoped for ARM (management.azure.com).
// Used for the WebSocket tunnel auth header.
func (c *Client) GetARMToken(ctx context.Context) (string, error) {
	return c.getToken(ctx, ARMScope)
}

// GetTokenForScope returns a bearer token for an arbitrary scope. Exposed so
// callers (e.g. the SSH tunnel) can experiment with audiences without changing
// the client struct.
func (c *Client) GetTokenForScope(ctx context.Context, scope string) (string, error) {
	return c.getToken(ctx, scope)
}
