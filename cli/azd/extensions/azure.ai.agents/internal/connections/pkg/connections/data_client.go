// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package connections

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
)

const dataPlaneAPIVersion = "2025-11-15-preview"

// DataClient provides read operations via the Foundry data plane.
// Used for listing connections (including ARM ID discovery) and fetching credentials.
type DataClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewDataClient creates a new data-plane client for connection operations.
func NewDataClient(endpoint string, cred azcore.TokenCredential) *DataClient {
	clientOptions := &policy.ClientOptions{
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(
				cred,
				[]string{"https://ai.azure.com/.default"},
				nil,
			),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy("azd-ext-azure-ai-connection/0.1.0"),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-connection-data",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &DataClient{endpoint: endpoint, pipeline: pipeline}
}

// ListConnections retrieves all connections from the project via data-plane GET.
func (c *DataClient) ListConnections(ctx context.Context) ([]Connection, error) {
	var allConnections []Connection

	paged, err := c.getPage(
		ctx,
		fmt.Sprintf("%s/connections?api-version=%s", c.endpoint, dataPlaneAPIVersion),
	)
	if err != nil {
		return nil, err
	}

	allConnections = append(allConnections, paged.Value...)
	nextLink := paged.NextLink

	for nextLink != nil && *nextLink != "" {
		if err := c.validateNextLinkOrigin(*nextLink); err != nil {
			return nil, fmt.Errorf("refusing to follow pagination link: %w", err)
		}

		paged, err = c.getPage(ctx, *nextLink)
		if err != nil {
			return nil, err
		}

		allConnections = append(allConnections, paged.Value...)
		nextLink = paged.NextLink
	}

	return allConnections, nil
}

func (c *DataClient) validateNextLinkOrigin(nextLink string) error {
	endpointURL, err := url.Parse(c.endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	linkURL, err := url.Parse(nextLink)
	if err != nil {
		return fmt.Errorf("invalid nextLink URL: %w", err)
	}

	if linkURL.Scheme == "" {
		return fmt.Errorf("nextLink must have an explicit scheme, got %q", nextLink)
	}

	if !strings.EqualFold(linkURL.Scheme, endpointURL.Scheme) ||
		!strings.EqualFold(linkURL.Host, endpointURL.Host) {
		return fmt.Errorf(
			"nextLink origin mismatch: expected %s://%s, got %s://%s",
			endpointURL.Scheme, endpointURL.Host, linkURL.Scheme, linkURL.Host,
		)
	}

	return nil
}

func (c *DataClient) getPage(ctx context.Context, targetURL string) (*PagedConnection, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, targetURL)
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

	var paged PagedConnection
	if err := json.Unmarshal(body, &paged); err != nil {
		return nil, fmt.Errorf("failed to unmarshal connections: %w", err)
	}

	return &paged, nil
}

// GetConnectionWithCredentials retrieves a specific connection with its credentials
// via the data-plane POST endpoint.
func (c *DataClient) GetConnectionWithCredentials(
	ctx context.Context,
	name string,
) (*Connection, error) {
	targetURL := fmt.Sprintf(
		"%s/connections/%s/getConnectionWithCredentials?api-version=%s",
		c.endpoint, url.PathEscape(name), dataPlaneAPIVersion,
	)

	req, err := runtime.NewRequest(ctx, http.MethodPost, targetURL)
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

	var raw struct {
		Name        string            `json:"name"`
		ID          string            `json:"id"`
		Type        string            `json:"type"`
		Target      string            `json:"target"`
		IsDefault   bool              `json:"isDefault"`
		Credentials map[string]any    `json:"credentials"`
		Metadata    map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal connection: %w", err)
	}

	conn := &Connection{
		Name:        raw.Name,
		ID:          raw.ID,
		Type:        raw.Type,
		Target:      raw.Target,
		IsDefault:   raw.IsDefault,
		Credentials: ParseCredentials(raw.Credentials),
		Metadata:    raw.Metadata,
	}

	return conn, nil
}
