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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	DefaultAPIVersion = "2026-01-15-preview"
	DatasetAPIVersion = "v1"
	DataPlaneScope    = "https://ai.azure.com/.default"
	ARMScope          = "https://management.azure.com/.default"
)

// Client is an HTTP client for Azure AI Foundry project APIs.
type Client struct {
	baseURL    string
	subPath    string
	apiVersion string
	credential azcore.TokenCredential
	httpClient *http.Client
}

// NewClient creates a new client from a project endpoint URL.
// Endpoint format: https://{account}.services.ai.azure.com/api/projects/{project}
// Also supports: https://{account}.cognitiveservices.azure.com/api/projects/{project}
func NewClient(projectEndpoint string, credential azcore.TokenCredential) (*Client, error) {
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

	return &Client{
		baseURL:    baseURL,
		subPath:    subPath,
		apiVersion: DefaultAPIVersion,
		credential: credential,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// doDataPlaneWithVersion executes an authenticated HTTP request with a specific API version.
func (c *Client) doDataPlaneWithVersion(ctx context.Context, method, path, apiVersion string, body interface{}, queryParams ...string) (*http.Response, error) {
	reqURL := fmt.Sprintf("%s%s/%s?api-version=%s", c.baseURL, c.subPath, path, apiVersion)
	for i := 0; i+1 < len(queryParams); i += 2 {
		reqURL += fmt.Sprintf("&%s=%s", queryParams[i], url.QueryEscape(queryParams[i+1]))
	}

	fmt.Printf("[DEBUG] %s %s\n", method, reqURL)

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(ctx, req, DataPlaneScope); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// doDataPlane executes an authenticated HTTP request against the data plane.
func (c *Client) doDataPlane(ctx context.Context, method, path string, body interface{}, queryParams ...string) (*http.Response, error) {
	return c.doDataPlaneWithVersion(ctx, method, path, c.apiVersion, body, queryParams...)
}

// addAuth adds a bearer token to the request.
func (c *Client) addAuth(ctx context.Context, req *http.Request, scope string) error {
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{scope},
	})
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)
	return nil
}

// HandleError reads the error body and returns a formatted error.
func (c *Client) HandleError(resp *http.Response) error {
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
