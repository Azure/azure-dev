// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

// ListToolsets returns all toolsets in the Foundry project.
func (c *AgentClient) ListToolsets(ctx context.Context, apiVersion string) (*ToolsetList, error) {
	url := fmt.Sprintf("%s/toolsets?api-version=%s", c.endpoint, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", ToolsetFeatureHeader)

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var list ToolsetList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &list, nil
}

// GetToolset retrieves a specific toolset by name.
func (c *AgentClient) GetToolset(ctx context.Context, name, apiVersion string) (*ToolsetObject, error) {
	url := fmt.Sprintf("%s/toolsets/%s?api-version=%s", c.endpoint, name, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", ToolsetFeatureHeader)

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var toolset ToolsetObject
	if err := json.Unmarshal(body, &toolset); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &toolset, nil
}

// CreateToolset creates a new toolset.
func (c *AgentClient) CreateToolset(
	ctx context.Context, request *CreateToolsetRequest, apiVersion string,
) (*ToolsetObject, error) {
	url := fmt.Sprintf("%s/toolsets?api-version=%s", c.endpoint, apiVersion)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", ToolsetFeatureHeader)

	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
		return nil, fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var toolset ToolsetObject
	if err := json.Unmarshal(body, &toolset); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &toolset, nil
}

// UpdateToolset updates an existing toolset by name.
func (c *AgentClient) UpdateToolset(
	ctx context.Context, name string, request *UpdateToolsetRequest, apiVersion string,
) (*ToolsetObject, error) {
	url := fmt.Sprintf("%s/toolsets/%s?api-version=%s", c.endpoint, name, apiVersion)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", ToolsetFeatureHeader)

	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
		return nil, fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var toolset ToolsetObject
	if err := json.Unmarshal(body, &toolset); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &toolset, nil
}

// DeleteToolset deletes a toolset by name.
func (c *AgentClient) DeleteToolset(ctx context.Context, name, apiVersion string) (*DeleteToolsetResponse, error) {
	url := fmt.Sprintf("%s/toolsets/%s?api-version=%s", c.endpoint, name, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodDelete, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", ToolsetFeatureHeader)

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var deleteResponse DeleteToolsetResponse
	if err := json.Unmarshal(body, &deleteResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &deleteResponse, nil
}
