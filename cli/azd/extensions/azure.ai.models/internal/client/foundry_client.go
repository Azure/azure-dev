// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"azure.ai.models/pkg/models"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	DefaultAPIVersion = "2025-11-15-preview"
	TokenScope        = "https://ai.azure.com/.default"
)

// FoundryClient is an HTTP client for Azure AI Foundry project APIs.
type FoundryClient struct {
	baseURL    string
	subPath    string
	apiVersion string
	credential azcore.TokenCredential
	httpClient *http.Client
}

// NewFoundryClient creates a new client from a project endpoint URL.
// Endpoint format: https://{account}.services.ai.azure.com/api/projects/{project}
func NewFoundryClient(projectEndpoint string, credential azcore.TokenCredential) (*FoundryClient, error) {
	parsedURL, err := url.Parse(projectEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid project endpoint URL: %w", err)
	}

	// Validate the URL structure
	hostname := parsedURL.Hostname()
	if hostname == "" {
		return nil, fmt.Errorf("invalid project endpoint URL: missing hostname")
	}

	pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	if len(pathParts) < 3 || pathParts[0] != "api" || pathParts[1] != "projects" || pathParts[2] == "" {
		return nil, fmt.Errorf("invalid project endpoint URL: expected format https://{account}.services.ai.azure.com/api/projects/{project}")
	}

	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
	subPath := "/" + strings.Join(pathParts[:3], "/")

	return &FoundryClient{
		baseURL:    baseURL,
		subPath:    subPath,
		apiVersion: DefaultAPIVersion,
		credential: credential,
		httpClient: &http.Client{},
	}, nil
}

// ListModels lists all custom models in the project.
func (c *FoundryClient) ListModels(ctx context.Context) (*models.ListModelsResponse, error) {
	reqURL := fmt.Sprintf("%s%s/models?api-version=%s", c.baseURL, c.subPath, c.apiVersion)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	var result models.ListModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// StartPendingUpload initiates a pending upload for a custom model version.
// POST {subPath}/models/{modelName}/versions/{version}/startPendingUpload
func (c *FoundryClient) StartPendingUpload(ctx context.Context, modelName, version string) (*models.PendingUploadResponse, error) {
	reqURL := fmt.Sprintf("%s%s/models/%s/versions/%s/startPendingUpload?api-version=%s",
		c.baseURL, c.subPath,
		url.PathEscape(modelName), url.PathEscape(version),
		c.apiVersion,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	var result models.PendingUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// RegisterModel registers a custom model version after upload.
// PUT {subPath}/models/{modelName}/versions/{version}
func (c *FoundryClient) RegisterModel(ctx context.Context, modelName, version string, req *models.RegisterModelRequest) (*models.CustomModel, error) {
	reqURL := fmt.Sprintf("%s%s/models/%s/versions/%s?api-version=%s",
		c.baseURL, c.subPath,
		url.PathEscape(modelName), url.PathEscape(version),
		c.apiVersion,
	)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(ctx, httpReq); err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, c.handleError(resp)
	}

	var result models.CustomModel
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// DeleteModel deletes a custom model version.
// DELETE {subPath}/models/{modelName}/versions/{version}
func (c *FoundryClient) DeleteModel(ctx context.Context, modelName, version string) error {
	reqURL := fmt.Sprintf("%s%s/models/%s/versions/%s?api-version=%s",
		c.baseURL, c.subPath,
		url.PathEscape(modelName), url.PathEscape(version),
		c.apiVersion,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(ctx, req); err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.handleError(resp)
	}

	return nil
}
func (c *FoundryClient) addAuth(ctx context.Context, req *http.Request) error {
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{TokenScope},
	})
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)
	return nil
}

// handleError reads the error body and returns a formatted error.
func (c *FoundryClient) handleError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	var apiErr struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
		return fmt.Errorf("API error (%d): %s - %s", resp.StatusCode, apiErr.Error.Code, apiErr.Error.Message)
	}

	return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
}
