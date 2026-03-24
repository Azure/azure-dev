// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"azureaiagent/internal/version"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

const (
	toolsetsApiVersion    = "v1"
	toolsetsFeatureHeader = "Toolsets=V1Preview"
)

// FoundryToolsetsClient provides methods for interacting with the Foundry Toolsets API
type FoundryToolsetsClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewFoundryToolsetsClient creates a new FoundryToolsetsClient
func NewFoundryToolsetsClient(
	endpoint string,
	cred azcore.TokenCredential,
) *FoundryToolsetsClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{azsdk.MsCorrelationIdHeader},
			IncludeBody:    true,
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(userAgent),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-agents",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &FoundryToolsetsClient{
		endpoint: endpoint,
		pipeline: pipeline,
	}
}

// CreateToolsetRequest is the request body for creating a toolset
type CreateToolsetRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Tools       []map[string]any  `json:"tools"`
}

// UpdateToolsetRequest is the request body for updating a toolset
type UpdateToolsetRequest struct {
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Tools       []map[string]any  `json:"tools"`
}

// ToolsetObject is the response object for a toolset
type ToolsetObject struct {
	Object      string            `json:"object"`
	Id          string            `json:"id"`
	CreatedAt   int64             `json:"created_at"`
	UpdatedAt   int64             `json:"updated_at"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Tools       []map[string]any  `json:"tools"`
}

// DeleteToolsetResponse is the response for deleting a toolset
type DeleteToolsetResponse struct {
	Object  string `json:"object"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// CreateToolset creates a new toolset
func (c *FoundryToolsetsClient) CreateToolset(
	ctx context.Context,
	request *CreateToolsetRequest,
) (*ToolsetObject, error) {
	targetUrl := fmt.Sprintf(
		"%s/toolsets?api-version=%s",
		c.endpoint, toolsetsApiVersion,
	)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, targetUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", toolsetsFeatureHeader)

	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(payload)),
		"application/json",
	); err != nil {
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

// UpdateToolset updates an existing toolset
func (c *FoundryToolsetsClient) UpdateToolset(
	ctx context.Context,
	toolsetName string,
	request *UpdateToolsetRequest,
) (*ToolsetObject, error) {
	targetUrl := fmt.Sprintf(
		"%s/toolsets/%s?api-version=%s",
		c.endpoint, url.PathEscape(toolsetName), toolsetsApiVersion,
	)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, targetUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", toolsetsFeatureHeader)

	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(payload)),
		"application/json",
	); err != nil {
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

// GetToolset retrieves a toolset by name
func (c *FoundryToolsetsClient) GetToolset(
	ctx context.Context,
	toolsetName string,
) (*ToolsetObject, error) {
	targetUrl := fmt.Sprintf(
		"%s/toolsets/%s?api-version=%s",
		c.endpoint, url.PathEscape(toolsetName), toolsetsApiVersion,
	)

	req, err := runtime.NewRequest(ctx, http.MethodGet, targetUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", toolsetsFeatureHeader)

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
