// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"azureaiagent/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// AgentClient provides methods for interacting with the Azure AI Agents API
type AgentClient struct {
	endpoint   string
	pipeline   runtime.Pipeline
	credential azcore.TokenCredential
}

// NewAgentClient creates a new AgentClient
func NewAgentClient(endpoint string, cred azcore.TokenCredential) *AgentClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{"X-Ms-Correlation-Request-Id", "X-Request-Id"},
			// Include request/response bodies in logs when debug mode is enabled.
			// Sensitive data is sanitized in internal/cmd/debug.go.
			IncludeBody: true,
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

	return &AgentClient{
		endpoint:   endpoint,
		pipeline:   pipeline,
		credential: cred,
	}
}

// GetAgent retrieves a specific agent by name
func (c *AgentClient) GetAgent(ctx context.Context, agentName, apiVersion string) (*AgentObject, error) {
	url := fmt.Sprintf("%s/agents/%s?api-version=%s", c.endpoint, agentName, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
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

	var agent AgentObject
	if err := json.Unmarshal(body, &agent); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &agent, nil
}

// CreateAgent creates a new agent
func (c *AgentClient) CreateAgent(ctx context.Context, request *CreateAgentRequest, apiVersion string) (*AgentObject, error) {
	url := fmt.Sprintf("%s/agents?api-version=%s", c.endpoint, apiVersion)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
		return nil, fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var agent AgentObject
	if err := json.Unmarshal(body, &agent); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &agent, nil
}

// UpdateAgent updates an existing agent
func (c *AgentClient) UpdateAgent(ctx context.Context, agentName string, request *UpdateAgentRequest, apiVersion string) (*AgentObject, error) {
	url := fmt.Sprintf("%s/agents/%s?api-version=%s", c.endpoint, agentName, apiVersion)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
		return nil, fmt.Errorf("failed to set request body: %w", err)
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

	var agent AgentObject
	if err := json.Unmarshal(body, &agent); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &agent, nil
}

// DeleteAgent deletes an agent
func (c *AgentClient) DeleteAgent(ctx context.Context, agentName, apiVersion string) (*DeleteAgentResponse, error) {
	url := fmt.Sprintf("%s/agents/%s?api-version=%s", c.endpoint, agentName, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodDelete, url)
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

	var deleteResponse DeleteAgentResponse
	if err := json.Unmarshal(body, &deleteResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &deleteResponse, nil
}

// ListAgents returns a list of all agents
func (c *AgentClient) ListAgents(ctx context.Context, params *ListAgentQueryParameters, apiVersion string) (*AgentList, error) {
	baseURL := fmt.Sprintf("%s/agents", c.endpoint)

	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse base URL: %w", err)
	}

	query := u.Query()
	query.Set("api-version", apiVersion)

	if params != nil {
		if params.Kind != nil {
			query.Set("kind", string(*params.Kind))
		}
		if params.Limit != nil {
			query.Set("limit", strconv.Itoa(int(*params.Limit)))
		}
		if params.After != nil {
			query.Set("after", *params.After)
		}
		if params.Before != nil {
			query.Set("before", *params.Before)
		}
		if params.Order != nil {
			query.Set("order", *params.Order)
		}
	}

	u.RawQuery = query.Encode()

	req, err := runtime.NewRequest(ctx, http.MethodGet, u.String())
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

	var agentList AgentList
	if err := json.Unmarshal(body, &agentList); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &agentList, nil
}

// CreateAgentVersion creates a new version of an agent
func (c *AgentClient) CreateAgentVersion(ctx context.Context, agentName string, request *CreateAgentVersionRequest, apiVersion string) (*AgentVersionObject, error) {
	url := fmt.Sprintf("%s/agents/%s/versions?api-version=%s", c.endpoint, agentName, apiVersion)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
		return nil, fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var agentVersion AgentVersionObject
	if err := json.Unmarshal(body, &agentVersion); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &agentVersion, nil
}

// GetAgentVersion retrieves a specific version of an agent
func (c *AgentClient) GetAgentVersion(ctx context.Context, agentName, agentVersion, apiVersion string) (*AgentVersionObject, error) {
	url := fmt.Sprintf("%s/agents/%s/versions/%s?api-version=%s", c.endpoint, agentName, agentVersion, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
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

	var version AgentVersionObject
	if err := json.Unmarshal(body, &version); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &version, nil
}

// DeleteAgentVersion deletes a specific version of an agent
func (c *AgentClient) DeleteAgentVersion(ctx context.Context, agentName, agentVersion, apiVersion string) (*DeleteAgentVersionResponse, error) {
	url := fmt.Sprintf("%s/agents/%s/versions/%s?api-version=%s", c.endpoint, agentName, agentVersion, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodDelete, url)
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

	var deleteResponse DeleteAgentVersionResponse
	if err := json.Unmarshal(body, &deleteResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &deleteResponse, nil
}

// Common query parameters for pagination
type CommonPageQueryParameters struct {
	Limit  *int32  `json:"limit,omitempty"`
	After  *string `json:"after,omitempty"`
	Before *string `json:"before,omitempty"`
	Order  *string `json:"order,omitempty"`
}

// ListAgentVersions returns a list of versions for a specific agent
func (c *AgentClient) ListAgentVersions(ctx context.Context, agentName string, params *CommonPageQueryParameters, apiVersion string) (*AgentVersionList, error) {
	baseURL := fmt.Sprintf("%s/agents/%s/versions", c.endpoint, agentName)

	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse base URL: %w", err)
	}

	query := u.Query()
	query.Set("api-version", apiVersion)

	if params != nil {
		if params.Limit != nil {
			query.Set("limit", strconv.Itoa(int(*params.Limit)))
		}
		if params.After != nil {
			query.Set("after", *params.After)
		}
		if params.Before != nil {
			query.Set("before", *params.Before)
		}
		if params.Order != nil {
			query.Set("order", *params.Order)
		}
	}

	u.RawQuery = query.Encode()

	req, err := runtime.NewRequest(ctx, http.MethodGet, u.String())
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

	var versionList AgentVersionList
	if err := json.Unmarshal(body, &versionList); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &versionList, nil
}

// Event Handler Operations

// CreateOrUpdateAgentEventHandler creates or updates an event handler for an agent
func (c *AgentClient) CreateOrUpdateAgentEventHandler(ctx context.Context, agentName, eventHandlerName string, request *AgentEventHandlerRequest, apiVersion string) (*AgentEventHandlerObject, error) {
	url := fmt.Sprintf("%s/agents/%s/event_handlers/%s?api-version=%s", c.endpoint, agentName, eventHandlerName, apiVersion)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
		return nil, fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var eventHandler AgentEventHandlerObject
	if err := json.Unmarshal(body, &eventHandler); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &eventHandler, nil
}

// GetAgentEventHandler retrieves a specific event handler
func (c *AgentClient) GetAgentEventHandler(ctx context.Context, agentName, eventHandlerName, apiVersion string) (*AgentEventHandlerObject, error) {
	url := fmt.Sprintf("%s/agents/%s/event_handlers/%s?api-version=%s", c.endpoint, agentName, eventHandlerName, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
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

	var eventHandler AgentEventHandlerObject
	if err := json.Unmarshal(body, &eventHandler); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &eventHandler, nil
}

// DeleteAgentEventHandler deletes an event handler
func (c *AgentClient) DeleteAgentEventHandler(ctx context.Context, agentName, eventHandlerName, apiVersion string) (*DeleteAgentEventHandlerResponse, error) {
	url := fmt.Sprintf("%s/agents/%s/event_handlers/%s?api-version=%s", c.endpoint, agentName, eventHandlerName, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodDelete, url)
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

	var deleteResponse DeleteAgentEventHandlerResponse
	if err := json.Unmarshal(body, &deleteResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &deleteResponse, nil
}

// cancelOnCloseReader wraps an io.ReadCloser and calls a cancel function when closed.
type cancelOnCloseReader struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelOnCloseReader) Close() error {
	r.cancel()
	return r.ReadCloser.Close()
}

// GetAgentSessionLogStream streams logs from an agent session.
// This uses the session-based logstream endpoint for agent configurations.
// kind should be "console" (stdout/stderr) or "system" (container events).
// tail is the number of trailing lines to fetch (1-300).
// follow controls whether to stream indefinitely (true) or fetch and exit (false).
func (c *AgentClient) GetAgentSessionLogStream(
	ctx context.Context,
	agentName, sessionID, apiVersion string,
	kind string,
	tail int,
	follow bool,
) (io.ReadCloser, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf("/agents/%s/sessions/%s:logstream", agentName, sessionID)

	query := u.Query()
	query.Set("api-version", apiVersion)
	query.Set("kind", kind)
	query.Set("tail", strconv.Itoa(tail))
	query.Set("follow", strconv.FormatBool(follow))
	u.RawQuery = query.Encode()

	requestURL := u.String()
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://ai.azure.com/.default"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	requestCtx := ctx
	var cancel context.CancelFunc
	if !follow {
		requestCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
	}

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("User-Agent", fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version))

	httpClient := &http.Client{}
	//nolint:gosec // request URL is built from trusted SDK endpoint + path components
	resp, err := httpClient.Do(req)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		return nil, fmt.Errorf("unexpected status code: %d — %s", resp.StatusCode, string(body))
	}

	if cancel != nil {
		return &cancelOnCloseReader{ReadCloser: resp.Body, cancel: cancel}, nil
	}

	return resp.Body, nil
}

// UploadSessionFile uploads a file to a session's filesystem.
// remotePath is the destination path on the session's filesystem.
// body is the file content to upload.
func (c *AgentClient) UploadSessionFile(
	ctx context.Context,
	agentName, sessionID, remotePath, apiVersion string,
	body io.ReadSeeker,
) error {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf(
		"/agents/%s/endpoint/sessions/%s/files/content",
		agentName, sessionID,
	)

	query := u.Query()
	query.Set("api-version", apiVersion)
	query.Set("path", remotePath)
	u.RawQuery = query.Encode()

	req, err := runtime.NewRequest(ctx, http.MethodPut, u.String())
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if err := req.SetBody(streaming.NopCloser(body), "application/octet-stream"); err != nil {
		return fmt.Errorf("failed to set request body: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", "HostedAgents=V1Preview")

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return runtime.NewResponseError(resp)
	}

	return nil
}

// DownloadSessionFile downloads a file from a session's filesystem.
// remotePath is the source path on the session's filesystem.
// Returns an io.ReadCloser with the file content; the caller must close it.
func (c *AgentClient) DownloadSessionFile(
	ctx context.Context,
	agentName, sessionID, remotePath, apiVersion string,
) (io.ReadCloser, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf(
		"/agents/%s/endpoint/sessions/%s/files/content",
		agentName, sessionID,
	)

	query := u.Query()
	query.Set("api-version", apiVersion)
	query.Set("path", remotePath)
	u.RawQuery = query.Encode()

	req, err := runtime.NewRequest(ctx, http.MethodGet, u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	runtime.SkipBodyDownload(req)

	req.Raw().Header.Set("Foundry-Features", "HostedAgents=V1Preview")

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		defer resp.Body.Close()
		return nil, runtime.NewResponseError(resp)
	}

	return resp.Body, nil
}

// ListSessionFiles lists files in a session's filesystem.
// remotePath is the directory path to list (empty string for root).
func (c *AgentClient) ListSessionFiles(
	ctx context.Context,
	agentName, sessionID, remotePath, apiVersion string,
) (*SessionFileList, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf(
		"/agents/%s/endpoint/sessions/%s/files",
		agentName, sessionID,
	)

	query := u.Query()
	query.Set("api-version", apiVersion)
	if remotePath != "" {
		query.Set("path", remotePath)
	}
	u.RawQuery = query.Encode()

	req, err := runtime.NewRequest(ctx, http.MethodGet, u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", "HostedAgents=V1Preview")

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var fileList SessionFileList
	if err := json.Unmarshal(respBody, &fileList); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &fileList, nil
}

// RemoveSessionFile removes a file or directory from a session's filesystem.
// remotePath is the path to remove.
// recursive controls whether to recursively remove directories.
func (c *AgentClient) RemoveSessionFile(
	ctx context.Context,
	agentName, sessionID, remotePath string,
	recursive bool,
	apiVersion string,
) error {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf(
		"/agents/%s/endpoint/sessions/%s/files",
		agentName, sessionID,
	)

	query := u.Query()
	query.Set("api-version", apiVersion)
	query.Set("path", remotePath)
	query.Set("recursive", strconv.FormatBool(recursive))
	u.RawQuery = query.Encode()

	req, err := runtime.NewRequest(ctx, http.MethodDelete, u.String())
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", "HostedAgents=V1Preview")

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusNoContent) {
		return runtime.NewResponseError(resp)
	}

	return nil
}

// MkdirSessionFile creates a directory in a session's filesystem.
func (c *AgentClient) MkdirSessionFile(
	ctx context.Context,
	agentName, sessionID, remotePath string,
	apiVersion string,
) error {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf(
		"/agents/%s/endpoint/sessions/%s/files/mkdir",
		agentName, sessionID,
	)

	query := u.Query()
	query.Set("api-version", apiVersion)
	u.RawQuery = query.Encode()

	body, err := json.Marshal(map[string]string{"path": remotePath})
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, u.String())
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Raw().Header.Set("Content-Type", "application/json")
	req.Raw().Header.Set("Foundry-Features", "HostedAgents=V1Preview")

	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(body)), "application/json"); err != nil {
		return fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated, http.StatusNoContent) {
		return runtime.NewResponseError(resp)
	}

	return nil
}

// StatSessionFile returns file/directory metadata from a session's filesystem.
func (c *AgentClient) StatSessionFile(
	ctx context.Context,
	agentName, sessionID, remotePath, apiVersion string,
) (*SessionFileInfo, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf(
		"/agents/%s/endpoint/sessions/%s/files/stat",
		agentName, sessionID,
	)

	query := u.Query()
	query.Set("api-version", apiVersion)
	query.Set("path", remotePath)
	u.RawQuery = query.Encode()

	req, err := runtime.NewRequest(ctx, http.MethodGet, u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", "HostedAgents=V1Preview")

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var fileInfo SessionFileInfo
	if err := json.Unmarshal(respBody, &fileInfo); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &fileInfo, nil
}

// ---------------------------------------------------------------------------
// Session Lifecycle Operations
// ---------------------------------------------------------------------------

// CreateSession creates a new session for an agent endpoint.
func (c *AgentClient) CreateSession(
	ctx context.Context,
	agentName, isolationKey string,
	request *CreateAgentSessionRequest,
	apiVersion string,
) (*AgentSessionResource, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf("/agents/%s/endpoint/sessions", agentName)

	query := u.Query()
	query.Set("api-version", apiVersion)
	u.RawQuery = query.Encode()

	if request == nil {
		request = &CreateAgentSessionRequest{}
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(payload)),
		"application/json",
	); err != nil {
		return nil, fmt.Errorf("failed to set request body: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", "AgentEndpoints=V1Preview")
	if isolationKey != "" {
		req.Raw().Header.Set("x-session-isolation-key", isolationKey)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var session AgentSessionResource
	if err := json.Unmarshal(body, &session); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &session, nil
}

// GetSession retrieves a session by ID.
func (c *AgentClient) GetSession(
	ctx context.Context,
	agentName, sessionID, apiVersion string,
) (*AgentSessionResource, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf(
		"/agents/%s/endpoint/sessions/%s",
		agentName, sessionID,
	)

	query := u.Query()
	query.Set("api-version", apiVersion)
	u.RawQuery = query.Encode()

	req, err := runtime.NewRequest(ctx, http.MethodGet, u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", "AgentEndpoints=V1Preview")

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

	var session AgentSessionResource
	if err := json.Unmarshal(body, &session); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &session, nil
}

// DeleteSession deletes a session synchronously.
func (c *AgentClient) DeleteSession(
	ctx context.Context,
	agentName, sessionID, isolationKey, apiVersion string,
) error {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf(
		"/agents/%s/endpoint/sessions/%s",
		agentName, sessionID,
	)

	query := u.Query()
	query.Set("api-version", apiVersion)
	u.RawQuery = query.Encode()

	req, err := runtime.NewRequest(ctx, http.MethodDelete, u.String())
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", "AgentEndpoints=V1Preview")
	if isolationKey != "" {
		req.Raw().Header.Set("x-session-isolation-key", isolationKey)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(
		resp, http.StatusOK, http.StatusNoContent,
	) {
		return runtime.NewResponseError(resp)
	}

	return nil
}

// ListSessions returns a list of sessions for the specified agent.
func (c *AgentClient) ListSessions(
	ctx context.Context,
	agentName string,
	limit *int32,
	paginationToken *string,
	apiVersion string,
) (*SessionListResult, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += fmt.Sprintf("/agents/%s/endpoint/sessions", agentName)

	query := u.Query()
	query.Set("api-version", apiVersion)
	if limit != nil {
		query.Set("limit", strconv.Itoa(int(*limit)))
	}
	if paginationToken != nil && *paginationToken != "" {
		query.Set("pagination_token", *paginationToken)
	}
	u.RawQuery = query.Encode()

	req, err := runtime.NewRequest(ctx, http.MethodGet, u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Raw().Header.Set("Foundry-Features", "AgentEndpoints=V1Preview")

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

	var result SessionListResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}
