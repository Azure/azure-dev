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
	"os"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// AgentClient provides methods for interacting with the Azure AI Agents API
type AgentClient struct {
	endpoint string
	cred     azcore.TokenCredential
	client   *http.Client
}

// NewAgentClient creates a new AgentClient
func NewAgentClient(endpoint string, cred azcore.TokenCredential) *AgentClient {
	return &AgentClient{
		endpoint: endpoint,
		cred:     cred,
		client:   &http.Client{},
	}
}

// GetAgent retrieves a specific agent by name
func (c *AgentClient) GetAgent(ctx context.Context, agentName, apiVersion string) (*AgentObject, error) {
	url := fmt.Sprintf("%s/agents/%s?api-version=%s", c.endpoint, agentName, apiVersion)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to get agent. Status code: %d, Response: %s", resp.StatusCode, string(body))
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

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	c.logRequest("POST", url, payload)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, fmt.Errorf("failed to create agent. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}

	var agent AgentObject
	if err := json.Unmarshal(body, &agent); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	c.logResponse(body)
	return &agent, nil
}

// UpdateAgent updates an existing agent
func (c *AgentClient) UpdateAgent(ctx context.Context, agentName string, request *UpdateAgentRequest, apiVersion string) (*AgentObject, error) {
	url := fmt.Sprintf("%s/agents/%s?api-version=%s", c.endpoint, agentName, apiVersion)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	c.logRequest("POST", url, payload)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to update agent. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}

	var agent AgentObject
	if err := json.Unmarshal(body, &agent); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	c.logResponse(body)
	return &agent, nil
}

// DeleteAgent deletes an agent
func (c *AgentClient) DeleteAgent(ctx context.Context, agentName, apiVersion string) (*DeleteAgentResponse, error) {
	url := fmt.Sprintf("%s/agents/%s?api-version=%s", c.endpoint, agentName, apiVersion)

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to delete agent. Status code: %d, Response: %s", resp.StatusCode, string(body))
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

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to list agents. Status code: %d, Response: %s", resp.StatusCode, string(body))
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

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	c.logRequest("POST", url, payload)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, fmt.Errorf("failed to create agent version. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}

	var agentVersion AgentVersionObject
	if err := json.Unmarshal(body, &agentVersion); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	c.logResponse(body)
	return &agentVersion, nil
}

// GetAgentVersion retrieves a specific version of an agent
func (c *AgentClient) GetAgentVersion(ctx context.Context, agentName, agentVersion, apiVersion string) (*AgentVersionObject, error) {
	url := fmt.Sprintf("%s/agents/%s/versions/%s?api-version=%s", c.endpoint, agentName, agentVersion, apiVersion)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to get agent version. Status code: %d, Response: %s", resp.StatusCode, string(body))
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

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to delete agent version. Status code: %d, Response: %s", resp.StatusCode, string(body))
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

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to list agent versions. Status code: %d, Response: %s", resp.StatusCode, string(body))
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

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, fmt.Errorf("failed to create/update event handler. Status code: %d, Response: %s", resp.StatusCode, string(body))
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

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to get event handler. Status code: %d, Response: %s", resp.StatusCode, string(body))
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

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to delete event handler. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}

	var deleteResponse DeleteAgentEventHandlerResponse
	if err := json.Unmarshal(body, &deleteResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &deleteResponse, nil
}

// Container Operations

// StartAgentContainer starts a container for a specific version of an agent
func (c *AgentClient) StartAgentContainer(ctx context.Context, agentName, agentVersion string, minReplicas, maxReplicas *int32, apiVersion string) (*AcceptedAgentContainerOperation, error) {
	url := fmt.Sprintf("%s/agents/%s/versions/%s/containers/default:start?api-version=%s", c.endpoint, agentName, agentVersion, apiVersion)

	requestBody := map[string]interface{}{}
	if minReplicas != nil {
		requestBody["min_replicas"] = *minReplicas
	}
	if maxReplicas != nil {
		requestBody["max_replicas"] = *maxReplicas
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	c.logRequest("POST", url, payload)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 202 {
		return nil, fmt.Errorf("failed to start agent container. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}

	var operation AgentContainerOperationObject
	if err := json.Unmarshal(body, &operation); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	result := &AcceptedAgentContainerOperation{
		Location: resp.Header.Get("Operation-Location"),
		Body:     operation,
	}

	c.logResponse(body)
	return result, nil
}

// UpdateAgentContainer updates a container for a specific version of an agent
func (c *AgentClient) UpdateAgentContainer(ctx context.Context, agentName, agentVersion string, minReplicas, maxReplicas *int32, apiVersion string) (*AcceptedAgentContainerOperation, error) {
	url := fmt.Sprintf("%s/agents/%s/versions/%s/containers/default:update?api-version=%s", c.endpoint, agentName, agentVersion, apiVersion)

	requestBody := map[string]interface{}{}
	if minReplicas != nil {
		requestBody["min_replicas"] = *minReplicas
	}
	if maxReplicas != nil {
		requestBody["max_replicas"] = *maxReplicas
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 202 {
		return nil, fmt.Errorf("failed to update agent container. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}

	var operation AgentContainerOperationObject
	if err := json.Unmarshal(body, &operation); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	result := &AcceptedAgentContainerOperation{
		Location: resp.Header.Get("Operation-Location"),
		Body:     operation,
	}

	return result, nil
}

// StopAgentContainer stops a container for a specific version of an agent
func (c *AgentClient) StopAgentContainer(ctx context.Context, agentName, agentVersion, apiVersion string) (*AcceptedAgentContainerOperation, error) {
	url := fmt.Sprintf("%s/agents/%s/versions/%s/containers/default:stop?api-version=%s", c.endpoint, agentName, agentVersion, apiVersion)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 202 {
		return nil, fmt.Errorf("failed to stop agent container. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}

	var operation AgentContainerOperationObject
	if err := json.Unmarshal(body, &operation); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	result := &AcceptedAgentContainerOperation{
		Location: resp.Header.Get("Operation-Location"),
		Body:     operation,
	}

	return result, nil
}

// DeleteAgentContainer deletes a container for a specific version of an agent
func (c *AgentClient) DeleteAgentContainer(ctx context.Context, agentName, agentVersion, apiVersion string) (*AcceptedAgentContainerOperation, error) {
	url := fmt.Sprintf("%s/agents/%s/versions/%s/containers/default:delete?api-version=%s", c.endpoint, agentName, agentVersion, apiVersion)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 202 {
		return nil, fmt.Errorf("failed to delete agent container. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}

	var operation AgentContainerOperationObject
	if err := json.Unmarshal(body, &operation); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	result := &AcceptedAgentContainerOperation{
		Location: resp.Header.Get("Operation-Location"),
		Body:     operation,
	}

	return result, nil
}

// GetAgentContainer retrieves container information for a specific agent version
func (c *AgentClient) GetAgentContainer(ctx context.Context, agentName, agentVersion, apiVersion string) (*AgentContainerObject, error) {
	url := fmt.Sprintf("%s/agents/%s/versions/%s/containers/default?api-version=%s", c.endpoint, agentName, agentVersion, apiVersion)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to get agent container. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}

	var container AgentContainerObject
	if err := json.Unmarshal(body, &container); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &container, nil
}

// GetAgentContainerOperation retrieves the status of a container operation
func (c *AgentClient) GetAgentContainerOperation(ctx context.Context, agentName, operationID, apiVersion string) (*AgentContainerOperationObject, error) {
	url := fmt.Sprintf("%s/agents/%s/operations/%s?api-version=%s", c.endpoint, agentName, operationID, apiVersion)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setAuthHeader(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to get container operation. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}

	var operation AgentContainerOperationObject
	if err := json.Unmarshal(body, &operation); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &operation, nil
}

// Helper methods

// setAuthHeader sets the authorization header using the credential
func (c *AgentClient) setAuthHeader(ctx context.Context, req *http.Request) error {
	token, err := c.getAiFoundryAzureToken(ctx, c.cred)
	if err != nil {
		return fmt.Errorf("failed to get Azure token: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// getAiFoundryAzureToken gets an Azure access token using the provided credential
func (c *AgentClient) getAiFoundryAzureToken(ctx context.Context, cred azcore.TokenCredential) (string, error) {
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
func (c *AgentClient) logRequest(method, url string, payload []byte) {
	fmt.Fprintf(os.Stderr, "%s %s\n", method, url)
	if len(payload) > 0 {
		var prettyPayload interface{}
		if err := json.Unmarshal(payload, &prettyPayload); err == nil {
			prettyJSON, _ := json.MarshalIndent(prettyPayload, "", "  ")
			fmt.Fprintf(os.Stderr, "Payload:\n%s\n", string(prettyJSON))
		} else {
			fmt.Fprintf(os.Stderr, "Payload: %s\n", string(payload))
		}
	}
}

// logResponse logs the response body to stderr for debugging
func (c *AgentClient) logResponse(body []byte) {
	fmt.Fprintln(os.Stderr, "Response:")
	var jsonResponse interface{}
	if err := json.Unmarshal(body, &jsonResponse); err == nil {
		prettyJSON, _ := json.MarshalIndent(jsonResponse, "", "  ")
		fmt.Fprintln(os.Stderr, string(prettyJSON))
	} else {
		fmt.Fprintln(os.Stderr, string(body))
	}
}
