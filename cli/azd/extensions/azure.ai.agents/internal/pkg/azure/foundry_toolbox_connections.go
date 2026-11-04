// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

const toolboxConnectionsAPIVersion = "2025-04-01-preview"

// ToolboxConnectionProperties describes the non-secret properties needed to
// authenticate an agent to a Toolbox MCP endpoint using the caller's token.
type ToolboxConnectionProperties struct {
	Category string            `json:"category"`
	AuthType string            `json:"authType"`
	Audience string            `json:"audience,omitempty"`
	Target   string            `json:"target"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type toolboxConnectionBody struct {
	Properties ToolboxConnectionProperties `json:"properties"`
}

// FoundryToolboxConnectionsClient reads a project's advertised endpoint and
// manages connection resources beneath that same ARM project.
type FoundryToolboxConnectionsClient struct {
	projectURL string
	pipeline   runtime.Pipeline
}

// NewFoundryToolboxConnectionsClient constructs a client scoped to a Foundry
// project resource ID. Nil options use the extension's standard ARM policies.
func NewFoundryToolboxConnectionsClient(
	projectID string, credential azcore.TokenCredential, options *policy.ClientOptions,
) (*FoundryToolboxConnectionsClient, error) {
	id, err := arm.ParseResourceID(projectID)
	if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") {
		return nil, fmt.Errorf("a Foundry project ARM resource ID is required for toolbox connections")
	}
	if options == nil {
		options = &NewArmClientOptions().ClientOptions
	}
	return &FoundryToolboxConnectionsClient{
		projectURL: "https://management.azure.com" + id.String(),
		pipeline: runtime.NewPipeline("azure-ai-agents", "v1.0.0", runtime.PipelineOptions{
			PerCall: []policy.Policy{runtime.NewBearerTokenPolicy(
				credential, []string{"https://management.azure.com/.default"}, nil,
			)},
		}, options),
	}, nil
}

// ProjectEndpoint returns the project's advertised AI Foundry API endpoint.
func (c *FoundryToolboxConnectionsClient) ProjectEndpoint(ctx context.Context) (string, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, c.projectURL+"?api-version="+toolboxConnectionsAPIVersion)
	if err != nil {
		return "", err
	}
	resp, err := c.pipeline.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", runtime.NewResponseError(resp)
	}
	var project struct {
		Properties struct {
			Endpoints map[string]string `json:"endpoints"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&project); err != nil {
		return "", fmt.Errorf("decoding Foundry project endpoint: %w", err)
	}
	return project.Properties.Endpoints["AI Foundry API"], nil
}

func (c *FoundryToolboxConnectionsClient) connectionURL(name string) string {
	return c.projectURL + "/connections/" + url.PathEscape(name) + "?api-version=" + toolboxConnectionsAPIVersion
}

// GetConnection returns a connection's properties, or nil when it does not exist.
func (c *FoundryToolboxConnectionsClient) GetConnection(
	ctx context.Context, name string,
) (*ToolboxConnectionProperties, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, c.connectionURL(name))
	if err != nil {
		return nil, err
	}
	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, runtime.NewResponseError(resp)
	}
	var body toolboxConnectionBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding toolbox connection: %w", err)
	}
	return &body.Properties, nil
}

// PutConnection writes connection properties with a body that can be resent on retries.
// The caller is responsible for checking compatibility before writing.
func (c *FoundryToolboxConnectionsClient) PutConnection(
	ctx context.Context, name string, properties ToolboxConnectionProperties,
) error {
	payload, err := json.Marshal(toolboxConnectionBody{Properties: properties})
	if err != nil {
		return err
	}
	req, err := runtime.NewRequest(ctx, http.MethodPut, c.connectionURL(name))
	if err != nil {
		return err
	}
	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
		return err
	}
	resp, err := c.pipeline.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return runtime.NewResponseError(resp)
	}
	return nil
}
