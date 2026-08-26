// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"

	"azureaiagent/internal/pkg/useragent"
)

const (
	toolboxesApiVersion = "v1"
)

// FoundryToolboxClient provides methods for interacting with the Foundry Toolboxes API.
type FoundryToolboxClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewFoundryToolboxClient creates a new FoundryToolboxClient.
func NewFoundryToolboxClient(
	endpoint string,
	cred azcore.TokenCredential,
) *FoundryToolboxClient {
	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{azsdk.MsCorrelationIdHeader, "X-Request-Id"},
			IncludeBody:    true,
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(useragent.Default()),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-agents",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &FoundryToolboxClient{
		endpoint: strings.TrimRight(endpoint, "/"),
		pipeline: pipeline,
	}
}

// ToolboxObject is the lightweight response for a toolbox (no tools list).
type ToolboxObject struct {
	Id             string `json:"id"`
	Name           string `json:"name"`
	DefaultVersion string `json:"default_version"`
}

// GetToolbox retrieves a toolbox by name.
func (c *FoundryToolboxClient) GetToolbox(
	ctx context.Context,
	toolboxName string,
) (*ToolboxObject, error) {
	targetUrl := fmt.Sprintf(
		"%s/toolboxes/%s?api-version=%s",
		c.endpoint, url.PathEscape(toolboxName), toolboxesApiVersion,
	)

	req, err := runtime.NewRequest(ctx, http.MethodGet, targetUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
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

	var result ToolboxObject
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// DeleteToolbox deletes a toolbox and all its versions.
func (c *FoundryToolboxClient) DeleteToolbox(
	ctx context.Context,
	toolboxName string,
) error {
	targetUrl := fmt.Sprintf(
		"%s/toolboxes/%s?api-version=%s",
		c.endpoint, url.PathEscape(toolboxName), toolboxesApiVersion,
	)

	req, err := runtime.NewRequest(ctx, http.MethodDelete, targetUrl)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusNoContent) {
		return runtime.NewResponseError(resp)
	}

	return nil
}
