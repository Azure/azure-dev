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

// ListToolboxes returns all toolboxes in the Foundry project.
func (c *AgentClient) ListToolboxes(ctx context.Context, apiVersion string) (*ToolboxList, error) {
	url := fmt.Sprintf("%s/toolsets?api-version=%s", c.endpoint, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", ToolboxFeatureHeader)

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

	var list ToolboxList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &list, nil
}

// GetToolbox retrieves a specific toolbox by name.
func (c *AgentClient) GetToolbox(ctx context.Context, name, apiVersion string) (*ToolboxObject, error) {
	url := fmt.Sprintf("%s/toolsets/%s?api-version=%s", c.endpoint, name, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", ToolboxFeatureHeader)

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

	var toolbox ToolboxObject
	if err := json.Unmarshal(body, &toolbox); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &toolbox, nil
}

// CreateToolbox creates a new toolbox.
func (c *AgentClient) CreateToolbox(
	ctx context.Context, request *CreateToolboxRequest, apiVersion string,
) (*ToolboxObject, error) {
	url := fmt.Sprintf("%s/toolsets?api-version=%s", c.endpoint, apiVersion)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", ToolboxFeatureHeader)

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

	var toolbox ToolboxObject
	if err := json.Unmarshal(body, &toolbox); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &toolbox, nil
}

// UpdateToolbox updates an existing toolbox by name.
func (c *AgentClient) UpdateToolbox(
	ctx context.Context, name string, request *UpdateToolboxRequest, apiVersion string,
) (*ToolboxObject, error) {
	url := fmt.Sprintf("%s/toolsets/%s?api-version=%s", c.endpoint, name, apiVersion)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", ToolboxFeatureHeader)

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

	var toolbox ToolboxObject
	if err := json.Unmarshal(body, &toolbox); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &toolbox, nil
}

// DeleteToolbox deletes a toolbox by name.
func (c *AgentClient) DeleteToolbox(ctx context.Context, name, apiVersion string) (*DeleteToolboxResponse, error) {
	url := fmt.Sprintf("%s/toolsets/%s?api-version=%s", c.endpoint, name, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodDelete, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", ToolboxFeatureHeader)

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

	var deleteResp DeleteToolboxResponse
	if err := json.Unmarshal(body, &deleteResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &deleteResp, nil
}
