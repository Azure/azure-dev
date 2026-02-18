// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"azure.ai.models/pkg/models"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	DefaultAPIVersion = "2025-11-15-preview"
	TokenScope        = "https://ai.azure.com/.default"
	ARMTokenScope     = "https://management.azure.com/.default"
	MLTokenScope      = "https://ml.azure.com/.default"
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

	// Enforce HTTPS to prevent sending bearer tokens over plaintext
	if !strings.EqualFold(parsedURL.Scheme, "https") {
		return nil, fmt.Errorf("invalid project endpoint URL: scheme must be https")
	}

	// Reject URLs with embedded credentials
	if parsedURL.User != nil {
		return nil, fmt.Errorf("invalid project endpoint URL: userinfo is not allowed")
	}

	// Validate the URL structure
	hostname := parsedURL.Hostname()
	if hostname == "" {
		return nil, fmt.Errorf("invalid project endpoint URL: missing hostname")
	}

	pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	if len(pathParts) != 3 || pathParts[0] != "api" || pathParts[1] != "projects" || pathParts[2] == "" {
		return nil, fmt.Errorf("invalid project endpoint URL: expected format https://{account}.services.ai.azure.com/api/projects/{project}")
	}

	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
	subPath := "/" + strings.Join(pathParts[:3], "/")

	return &FoundryClient{
		baseURL:    baseURL,
		subPath:    subPath,
		apiVersion: DefaultAPIVersion,
		credential: credential,
		httpClient: &http.Client{Timeout: 30 * time.Second},
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(body))
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

// RegisterModelAsync starts an async model creation with server-side validation.
// POST {subPath}/models/{modelName}/versions/{version}/createAsync
// Returns the polling Location URL from the 202 Accepted response.
func (c *FoundryClient) RegisterModelAsync(ctx context.Context, modelName, version string, req *models.RegisterModelRequest) (string, error) {
	reqURL := fmt.Sprintf("%s%s/models/%s/versions/%s/createAsync?api-version=%s",
		c.baseURL, c.subPath,
		url.PathEscape(modelName), url.PathEscape(version),
		c.apiVersion,
	)

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Disable redirects so we can capture the 202 + Location header
	noRedirectClient := &http.Client{
		Timeout: c.httpClient.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(ctx, httpReq); err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := noRedirectClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return "", c.handleError(resp)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		// Fall back to parsing Location from JSON body
		var asyncResp models.CreateAsyncResponse
		if err := json.NewDecoder(resp.Body).Decode(&asyncResp); err == nil && asyncResp.Location != "" {
			location = asyncResp.Location
		}
	}

	if location == "" {
		return "", fmt.Errorf("server returned 202 but no Location header or body for polling")
	}

	return location, nil
}

// PollOperation polls the given operation URL until it completes or the context is cancelled.
// The operation URL uses ARM-style auth, so a separate credential with ARM scope is required.
// Returns the completed CustomModel on success.
func (c *FoundryClient) PollOperation(ctx context.Context, operationURL string, pollInterval time.Duration) (*models.CustomModel, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, operationURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create poll request: %w", err)
		}

		// The operations endpoint on api.azureml.ms expects tokens with ml.azure.com audience
		token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
			Scopes: []string{MLTokenScope},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get ARM access token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token.Token)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("poll request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read poll response: %w", err)
		}

		if resp.StatusCode == http.StatusAccepted {
			// Still in progress
			if newLocation := resp.Header.Get("Location"); newLocation != "" {
				operationURL = newLocation
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("poll error (%d): %s", resp.StatusCode, string(body))
		}

		// 200 OK — check if it's an async status response or the final model
		var asyncStatus struct {
			Status string `json:"status"`
			Error  *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &asyncStatus) == nil && asyncStatus.Status != "" {
			switch asyncStatus.Status {
			case "InProgress", "NotStarted":
				continue
			case "Failed":
				if asyncStatus.Error != nil {
					return nil, fmt.Errorf("operation failed: %s - %s", asyncStatus.Error.Code, asyncStatus.Error.Message)
				}
				return nil, fmt.Errorf("operation failed without details")
			case "Succeeded":
				// For succeeded, the body is the FoundryModelDto directly — fall through to decode
				// But the server may return the status wrapper; try to get the model via GET
				var model models.CustomModel
				if json.Unmarshal(body, &model) == nil && model.Name != "" {
					return &model, nil
				}
				// Model not in the body — fetch it using the data-plane API
				return c.GetModel(ctx, c.extractModelName(operationURL), c.extractModelVersion(operationURL))
			}
		}

		// Direct model response (succeeded case from GetFoundryModelResponseObject)
		var result models.CustomModel
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("failed to decode completed operation: %w", err)
		}

		return &result, nil
	}
}

// extractModelName extracts the model name from an operation URL path.
func (c *FoundryClient) extractModelName(operationURL string) string {
	// URL format: .../models/{name}/versions/{version}/createAsync/operations/{id}
	parsed, err := url.Parse(operationURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(parsed.Path, "/")
	for i, p := range parts {
		if p == "models" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// extractModelVersion extracts the model version from an operation URL path.
func (c *FoundryClient) extractModelVersion(operationURL string) string {
	parsed, err := url.Parse(operationURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(parsed.Path, "/")
	for i, p := range parts {
		if p == "versions" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
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

// GetModel retrieves details of a specific custom model version.
// GET {subPath}/models/{modelName}/versions/{version}
func (c *FoundryClient) GetModel(ctx context.Context, modelName, version string) (*models.CustomModel, error) {
	reqURL := fmt.Sprintf("%s%s/models/%s/versions/%s?api-version=%s",
		c.baseURL, c.subPath,
		url.PathEscape(modelName), url.PathEscape(version),
		c.apiVersion,
	)

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

	var result models.CustomModel
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
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
