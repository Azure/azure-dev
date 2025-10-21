// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package registry_api

import (
	"context"
	"encoding/json"
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
func (c *RegistryAgentManifestClient) GetManifest(ctx context.Context, manifestName string, manifestVersion string) (*Manifest, error) {
	targetEndpoint := fmt.Sprintf("%s/%s/versions/%s", c.baseEndpoint, manifestName, manifestVersion)

	req, err := http.NewRequestWithContext(ctx, "GET", targetEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	fmt.Println("Making HTTP request to retrieve manifest...")
	body, err := c.makeHTTPRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest response: %w", err)
	}

	return &manifest, nil
}

// GetAllLatest retrieves all latest agent manifests from the specified registry
func (c *RegistryAgentManifestClient) GetAllLatest(ctx context.Context) ([]Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	body, err := c.makeHTTPRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	var manifestList ManifestList
	if err := json.Unmarshal(body, &manifestList); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest list response: %w", err)
	}

	return manifestList.Value, nil
}

// Helper methods

// makeHTTPRequest makes an HTTP request with proper authentication and error handling
func (c *RegistryAgentManifestClient) makeHTTPRequest(ctx context.Context, req *http.Request) ([]byte, error) {
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

// logRequest logs the request details to stderr for debugging
func (c *RegistryAgentManifestClient) logRequest(method, url string, payload []byte) {
	fmt.Printf("%s %s\n", method, url)
	if len(payload) > 0 {
		var prettyPayload interface{}
		if err := json.Unmarshal(payload, &prettyPayload); err == nil {
			prettyJSON, _ := json.MarshalIndent(prettyPayload, "", "  ")
			fmt.Printf("Payload:\n%s\n", string(prettyJSON))
		} else {
			fmt.Printf("Payload: %s\n", string(payload))
		}
	}
}

// logResponse logs the response body to stderr for debugging
func (c *RegistryAgentManifestClient) logResponse(body []byte) {
	fmt.Println("Response:")
	var jsonResponse interface{}
	if err := json.Unmarshal(body, &jsonResponse); err == nil {
		prettyJSON, _ := json.MarshalIndent(jsonResponse, "", "  ")
		fmt.Println(string(prettyJSON))
	} else {
		fmt.Println(string(body))
	}
}
