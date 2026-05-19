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
	"strconv"

	"azure.ai.training/pkg/models"
)

// GetServiceInstance retrieves service instance details for a specific node of a run.
//
//	GET .../jobs/{name}/history/runs/{runId}/serviceinstances/{nodeId}
//
// For Jobs, runId matches the job name.
// Returns nil with no error when the node does not exist (404).
func (c *Client) GetServiceInstance(
	ctx context.Context,
	jobName string,
	nodeIndex int,
) (*models.ServiceInstance, error) {
	path := fmt.Sprintf(
		"jobs/%s/history/runs/%s/serviceinstances/%s",
		url.PathEscape(jobName),
		url.PathEscape(jobName),
		strconv.Itoa(nodeIndex),
	)

	resp, err := c.doDataPlane(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get service instance: %w", err)
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
	jobName string,
	nodeIndex int,
) (json.RawMessage, error) {
	path := fmt.Sprintf(
		"jobs/%s/history/runs/%s/serviceinstances/%s",
		url.PathEscape(jobName),
		url.PathEscape(jobName),
		strconv.Itoa(nodeIndex),
	)

	resp, err := c.doDataPlane(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get service instance: %w", err)
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
