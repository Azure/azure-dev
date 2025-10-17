// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agents

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// RegistryAgentManifestClient provides methods to interact with Azure ML registry agent manifests
type RegistryAgentManifestClient struct {
	baseEndpoint string
	cred         azcore.TokenCredential
	client       *http.Client
}

// NewRegistryAgentManifestClient creates a new instance of RegistryAgentManifestClient
func NewRegistryAgentManifestClient(registryName string, cred azcore.TokenCredential) *RegistryAgentManifestClient {
	baseEndpoint := fmt.Sprintf("https://int.api.azureml-test.ms/agent-asset/v1.0/registries/%s/agentManifests", registryName)
	return &RegistryAgentManifestClient{
		baseEndpoint: baseEndpoint,
		cred:         cred,
		client:       &http.Client{},
	}
}

// GetManifest retrieves a specific agent manifest from the registry
func (c *RegistryAgentManifestClient) GetManifest(ctx context.Context, manifestName string, manifestVersion string) ([]byte, error) {
	// TODO: Implement the logic to retrieve the manifest from Azure ML registry
	// This would typically involve making an HTTP request to the Azure ML registry API
	// using the c.registryName, manifestName, and manifestVersion
	return nil, nil
}

// GetAllLatest retrieves all latest agent manifests from the specified registry
func (c *RegistryAgentManifestClient) GetAllLatest(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	return c.makeHTTPRequest(ctx, req)
}

// Helper methods

// makeHTTPRequest makes an HTTP request with proper authentication and error handling
func (c *RegistryAgentManifestClient) makeHTTPRequest(ctx context.Context, req *http.Request) ([]byte, error) {
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

	// Check for HTTP errors
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// setAuthHeader sets the authorization header using the credential
func (c *RegistryAgentManifestClient) setAuthHeader(ctx context.Context, req *http.Request) error {
	token, err := c.getAiFoundryAzureToken(ctx, c.cred)
	if err != nil {
		return fmt.Errorf("failed to get Azure token: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// getAiFoundryAzureToken gets an Azure access token using the provided credential
func (c *RegistryAgentManifestClient) getAiFoundryAzureToken(ctx context.Context, cred azcore.TokenCredential) (string, error) {
	tokenRequestOptions := policy.TokenRequestOptions{
		Scopes: []string{"https://ai.azure.com/.default"},
	}

	token, err := cred.GetToken(ctx, tokenRequestOptions)
	if err != nil {
		return "", err
	}

	return token.Token, nil
}
