// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// FoundryProjectsClient provides methods to interact with Microsoft Foundry projects
type FoundryProjectsClient struct {
	baseEndpoint string
	apiVersion   string
	cred         azcore.TokenCredential
	client       *http.Client
}

// NewFoundryProjectsClient creates a new instance of FoundryProjectsClient
func NewFoundryProjectsClient(accountName string, projectName string, cred azcore.TokenCredential) *FoundryProjectsClient {
	baseEndpoint := fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", accountName, projectName)
	return &FoundryProjectsClient{
		baseEndpoint: baseEndpoint,
		apiVersion:   "2025-11-15-preview",
		cred:         cred,
		client:       &http.Client{},
	}
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

	req, err := http.NewRequestWithContext(ctx, "GET", targetEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	body, err := c.makeHTTPRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	var pagedConnections PagedConnection
	if err := json.Unmarshal(body, &pagedConnections); err != nil {
		return nil, fmt.Errorf("failed to unmarshal connections response: %w", err)
	}

	return &pagedConnections, nil
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
		req, err := http.NewRequestWithContext(ctx, "GET", *nextLink, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request for next page: %w", err)
		}

		body, err := c.makeHTTPRequest(ctx, req)
		if err != nil {
			return nil, err
		}

		var pagedConnections PagedConnection
		if err := json.Unmarshal(body, &pagedConnections); err != nil {
			return nil, fmt.Errorf("failed to unmarshal connections response: %w", err)
		}

		// Add connections from this page
		allConnections = append(allConnections, pagedConnections.Value...)
		nextLink = pagedConnections.NextLink
	}

	return allConnections, nil
}

// Helper methods

// makeHTTPRequest makes an HTTP request with proper authentication and error handling
func (c *FoundryProjectsClient) makeHTTPRequest(ctx context.Context, req *http.Request) ([]byte, error) {
	// Log the request details - uncomment for debugging
	// c.logRequest(req.Method, req.URL.String(), nil)

	// Add authentication header
	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, fmt.Errorf("failed to set authentication header: %w", err)
	}

	// Set common headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Make the HTTP request
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Log the response details - uncomment for debugging
	// c.logResponse(body)

	// Check for HTTP errors
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// setAuthHeader sets the authorization header using the credential
func (c *FoundryProjectsClient) setAuthHeader(ctx context.Context, req *http.Request) error {
	token, err := c.getAiFoundryAzureToken(ctx, c.cred)
	if err != nil {
		return fmt.Errorf("failed to get Azure token: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// getAiFoundryAzureToken gets an Azure access token using the provided credential
func (c *FoundryProjectsClient) getAiFoundryAzureToken(ctx context.Context, cred azcore.TokenCredential) (string, error) {
	tokenRequestOptions := policy.TokenRequestOptions{
		Scopes: []string{"https://ai.azure.com/.default"},
	}

	token, err := cred.GetToken(ctx, tokenRequestOptions)
	if err != nil {
		return "", err
	}

	return token.Token, nil
}
