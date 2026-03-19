// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"azureaiagent/internal/version"
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

// FoundryProjectsClient provides methods to interact with Microsoft Foundry projects
type FoundryProjectsClient struct {
	baseEndpoint  string
	baseOriginURL *url.URL // cached parsed base URL for SSRF origin checks
	apiVersion    string
	pipeline      runtime.Pipeline
}

// NewFoundryProjectsClient creates a new instance of FoundryProjectsClient
func NewFoundryProjectsClient(
	accountName string,
	projectName string,
	cred azcore.TokenCredential,
) (*FoundryProjectsClient, error) {
	if strings.TrimSpace(accountName) == "" {
		return nil, fmt.Errorf("accountName must not be empty")
	}
	if strings.TrimSpace(projectName) == "" {
		return nil, fmt.Errorf("projectName must not be empty")
	}

	baseEndpoint := fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", accountName, projectName)

	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{azsdk.MsCorrelationIdHeader},
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

	parsedBase, err := url.Parse(baseEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid base endpoint URL: %w", err)
	}

	return &FoundryProjectsClient{
		baseEndpoint:  baseEndpoint,
		baseOriginURL: parsedBase,
		apiVersion:    "2025-11-15-preview",
		pipeline:      pipeline,
	}, nil
}

// Connection-related types

// ConnectionType represents the type/category of a connection
type ConnectionType string

const (
	ConnectionTypeAzureOpenAI         ConnectionType = "AzureOpenAI"
	ConnectionTypeAzureBlob           ConnectionType = "AzureBlob"
	ConnectionTypeAzureStorageAccount ConnectionType = "AzureStorageAccount"
	ConnectionTypeCognitiveSearch     ConnectionType = "CognitiveSearch"
	ConnectionTypeContainerRegistry   ConnectionType = "ContainerRegistry"
	ConnectionTypeCosmosDB            ConnectionType = "CosmosDB"
	ConnectionTypeApiKey              ConnectionType = "ApiKey"
	ConnectionTypeAppConfig           ConnectionType = "AppConfig"
	ConnectionTypeAppInsights         ConnectionType = "AppInsights"
	ConnectionTypeCustomKeys          ConnectionType = "CustomKeys"
	ConnectionTypeRemoteTool          ConnectionType = "RemoteTool"
)

// CredentialType represents the type of credential used by the connection
type CredentialType string

const (
	CredentialTypeApiKey               CredentialType = "ApiKey"
	CredentialTypeAAD                  CredentialType = "AAD"
	CredentialTypeCustomKeys           CredentialType = "CustomKeys"
	CredentialTypeSAS                  CredentialType = "SAS"
	CredentialTypeNone                 CredentialType = "None"
	CredentialTypeAgenticIdentityToken CredentialType = "AgenticIdentityToken"
)

// BaseCredentials represents the base class for connection credentials
type BaseCredentials struct {
	Type CredentialType `json:"type"`
	Key  string         `json:"key,omitempty"`
}

// Connection represents a connection response from the API
type Connection struct {
	Name        string            `json:"name"`
	ID          string            `json:"id"`
	Type        ConnectionType    `json:"type"`
	Target      string            `json:"target"`
	IsDefault   bool              `json:"isDefault"`
	Credentials BaseCredentials   `json:"credentials"`
	Metadata    map[string]string `json:"metadata"`
}

// PagedConnection represents a paged collection of Connection items
type PagedConnection struct {
	Value    []Connection `json:"value"`
	NextLink *string      `json:"nextLink,omitempty"`
}

// GetPagedConnections retrieves all connections from the project
func (c *FoundryProjectsClient) GetPagedConnections(ctx context.Context) (*PagedConnection, error) {
	targetEndpoint := fmt.Sprintf("%s/connections?api-version=%s", c.baseEndpoint, c.apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodGet, targetEndpoint)
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

	var pagedConnections PagedConnection
	if err := json.Unmarshal(body, &pagedConnections); err != nil {
		return nil, fmt.Errorf("failed to unmarshal connections response: %w", err)
	}

	return &pagedConnections, nil
}

// GetConnectionWithCredentials retrieves a specific connection with its credentials
func (c *FoundryProjectsClient) GetConnectionWithCredentials(ctx context.Context, name string) (*Connection, error) {
	targetEndpoint := fmt.Sprintf(
		"%s/connections/%s/getConnectionWithCredentials?api-version=%s",
		c.baseEndpoint, url.PathEscape(name), c.apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodPost, targetEndpoint)
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

	var connection Connection
	if err := json.Unmarshal(body, &connection); err != nil {
		return nil, fmt.Errorf("failed to unmarshal connection response: %w", err)
	}

	return &connection, nil
}

// GetAllConnections retrieves all connections from the project, handling pagination
func (c *FoundryProjectsClient) GetAllConnections(ctx context.Context) ([]Connection, error) {
	var allConnections []Connection
	var nextLink *string

	// Get the first page
	pagedResult, err := c.GetPagedConnections(ctx)
	if err != nil {
		return nil, err
	}

	// Add connections from the first page
	allConnections = append(allConnections, pagedResult.Value...)
	nextLink = pagedResult.NextLink

	// Continue fetching pages while there's a next link
	for nextLink != nil && *nextLink != "" {
		if err := c.validateNextLinkOrigin(*nextLink); err != nil {
			return nil, fmt.Errorf("refusing to follow pagination link: %w", err)
		}
		pagedConnections, err := c.getNextPage(ctx, *nextLink)
		if err != nil {
			return nil, err
		}

		// Add connections from this page
		allConnections = append(allConnections, pagedConnections.Value...)
		nextLink = pagedConnections.NextLink
	}

	return allConnections, nil
}

// validateNextLinkOrigin ensures that a pagination nextLink URL points to the same
// origin (scheme + host) as the client's base endpoint. This prevents SSRF attacks
// where a malicious API response redirects pagination to an attacker-controlled server.
func (c *FoundryProjectsClient) validateNextLinkOrigin(nextLink string) error {
	if c.baseOriginURL == nil {
		return fmt.Errorf("base endpoint URL not initialized")
	}

	linkURL, err := url.Parse(nextLink)
	if err != nil {
		return fmt.Errorf("invalid nextLink URL: %w", err)
	}

	// Reject scheme-relative URLs (e.g., "//evil.com/path") and URLs without an explicit scheme.
	// These could bypass origin checks or behave unpredictably.
	if linkURL.Scheme == "" {
		return fmt.Errorf("nextLink must have an explicit scheme, got %q", nextLink)
	}

	if !strings.EqualFold(linkURL.Scheme, c.baseOriginURL.Scheme) ||
		!strings.EqualFold(linkURL.Host, c.baseOriginURL.Host) {
		return fmt.Errorf(
			"nextLink origin mismatch: expected %s://%s, got %s://%s",
			c.baseOriginURL.Scheme, c.baseOriginURL.Host, linkURL.Scheme, linkURL.Host,
		)
	}

	return nil
}

// getNextPage fetches a single page of connections from the given URL
func (c *FoundryProjectsClient) getNextPage(ctx context.Context, url string) (*PagedConnection, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for next page: %w", err)
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

	var pagedConnections PagedConnection
	if err := json.Unmarshal(body, &pagedConnections); err != nil {
		return nil, fmt.Errorf("failed to unmarshal connections response: %w", err)
	}

	return &pagedConnections, nil
}
