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
	targetURL := fmt.Sprintf("%s/connections?api-version=%s", c.endpoint, dataPlaneAPIVersion)

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

	return paged.Value, nil
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

	var conn Connection
	if err := json.Unmarshal(body, &conn); err != nil {
		return nil, fmt.Errorf("failed to unmarshal connection: %w", err)
	}

	return &conn, nil
}
