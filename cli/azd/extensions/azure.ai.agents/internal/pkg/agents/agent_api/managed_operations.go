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
	"strings"

	"azureaiagent/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// ManagedAgentClient talks to the Foundry "managed" agent surface (the
// PES-backed Brain+Hand orchestration). It differs from AgentClient in two
// ways:
//
//  1. URLs are ARM-shaped — every operation is rooted at a workspace resource
//     (subscription / resourceGroup / workspace) rather than a Foundry project
//     endpoint.
//  2. Responses go through the v2.0 controller, which dispatches managed
//     agents to the V3 harness engine on the backend.
//
// The client is intentionally configured by a base URL plus a route prefix so
// callers can point it at either the production ARM control plane or a local
// development backend (e.g. the vienna "managed-harness" service running on
// http://localhost:5000) without leaking shape assumptions into this package.
type ManagedAgentClient struct {
	// baseURL is the scheme+host+optional-port of the service. No trailing slash.
	baseURL string
	// routePrefix is the URL segment between baseURL and the per-operation
	// suffix. It must NOT contain "/agents" — callers supply only the
	// workspace-rooted portion (e.g.
	// "/agents/v2.0/subscriptions/.../workspaces/<ws>"). No trailing slash.
	routePrefix string
	pipeline    runtime.Pipeline
	credential  azcore.TokenCredential
}

// ManagedAgentClientOptions are construction-time options for ManagedAgentClient.
type ManagedAgentClientOptions struct {
	// BaseURL is the service origin (e.g. "https://management.azure.com" or
	// "http://localhost:5000"). Required.
	BaseURL string
	// RoutePrefix is the ARM-style workspace prefix the backend expects between
	// the origin and the per-operation suffix. Must start with "/" and must
	// not end with "/". Example:
	//
	//	/agents/v2.0/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.MachineLearningServices/workspaces/<ws>
	//
	// Required.
	RoutePrefix string
	// Credential is the token credential used to acquire bearer tokens. May be
	// nil when targeting an unauthenticated local backend; in that case no
	// authorization policy is attached to the pipeline.
	Credential azcore.TokenCredential
	// Scopes are the OAuth scopes requested when Credential is non-nil.
	// Defaults to {"https://ai.azure.com/.default"}.
	Scopes []string
}

// NewManagedAgentClient builds a ManagedAgentClient from the given options.
// Returns an error when BaseURL or RoutePrefix is malformed.
func NewManagedAgentClient(opts ManagedAgentClientOptions) (*ManagedAgentClient, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("ManagedAgentClient: BaseURL is required")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("ManagedAgentClient: BaseURL %q is not a valid absolute URL", opts.BaseURL)
	}

	prefix := strings.TrimRight(strings.TrimSpace(opts.RoutePrefix), "/")
	if prefix == "" {
		return nil, fmt.Errorf("ManagedAgentClient: RoutePrefix is required")
	}
	if !strings.HasPrefix(prefix, "/") {
		return nil, fmt.Errorf("ManagedAgentClient: RoutePrefix %q must start with '/'", opts.RoutePrefix)
	}

	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	perCall := []policy.Policy{
		azsdk.NewMsCorrelationPolicy(),
		azsdk.NewUserAgentPolicy(userAgent),
	}
	if opts.Credential != nil {
		scopes := opts.Scopes
		if len(scopes) == 0 {
			scopes = []string{"https://ai.azure.com/.default"}
		}
		// The local managed-harness is served over plain HTTP
		// (http://localhost:5000) but still validates a bearer token. azcore
		// refuses to attach credentials to non-TLS endpoints unless this is
		// explicitly opted into, so allow it when (and only when) the base URL
		// is http — production https endpoints keep the default protection.
		var bearerOpts *policy.BearerTokenOptions
		if parsed.Scheme == "http" {
			bearerOpts = &policy.BearerTokenOptions{
				InsecureAllowCredentialWithHTTP: true,
			}
		}
		perCall = append([]policy.Policy{
			runtime.NewBearerTokenPolicy(opts.Credential, scopes, bearerOpts),
		}, perCall...)
	}

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{"X-Ms-Correlation-Request-Id", "X-Request-Id"},
			IncludeBody:    true,
		},
		PerCallPolicies: perCall,
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-agents-managed",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &ManagedAgentClient{
		baseURL:     base,
		routePrefix: prefix,
		pipeline:    pipeline,
		credential:  opts.Credential,
	}, nil
}

// agentsURL builds the URL for a managed-agents lifecycle operation. The
// optional pathSuffix is appended after "/agents" (it must start with "/" or
// be empty). Query parameters from extraQuery (which may include
// "api-version") are added to the final URL.
func (c *ManagedAgentClient) agentsURL(pathSuffix string, extraQuery url.Values) string {
	u := c.baseURL + c.routePrefix + "/agents" + pathSuffix
	if len(extraQuery) > 0 {
		u = u + "?" + extraQuery.Encode()
	}
	return u
}

// responsesURL builds the URL for an OpenAI-shape Responses operation. The
// Foundry project data-plane exposes a path-versioned OpenAI surface
// ("/openai/v1/responses") and rejects an api-version query parameter. The
// target agent travels in the request body as
// `agent_reference: { type: "agent_reference", name }`. pathSuffix is appended
// after "/openai/v1/responses" (must start with "/" or be empty).
func (c *ManagedAgentClient) responsesURL(pathSuffix string) string {
	return c.baseURL + c.routePrefix + "/openai/v1/responses" + pathSuffix
}

// CreateAgent creates a managed agent.
//
// POST {baseURL}{routePrefix}/agents?api-version=<apiVersion>
func (c *ManagedAgentClient) CreateAgent(
	ctx context.Context,
	request *CreateAgentRequest,
	apiVersion string,
) (*AgentObject, error) {
	return c.CreateAgentWithHeaders(ctx, request, apiVersion, nil)
}

// CreateAgentWithHeaders creates a managed agent and forwards any additional
// headers to the request. This is used by prompt-agent flows that need to
// pass backend routing hints such as x-model-endpoint.
func (c *ManagedAgentClient) CreateAgentWithHeaders(
	ctx context.Context,
	request *CreateAgentRequest,
	apiVersion string,
	headers map[string]string,
) (*AgentObject, error) {
	q := url.Values{}
	if apiVersion != "" {
		q.Set("api-version", apiVersion)
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, c.agentsURL("", q))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	for k, v := range headers {
		req.Raw().Header.Set(k, v)
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

// GetAgent retrieves a managed agent by name.
//
// GET {baseURL}{routePrefix}/agents/{name}?api-version=<apiVersion>
func (c *ManagedAgentClient) GetAgent(
	ctx context.Context,
	agentName, apiVersion string,
) (*AgentObject, error) {
	if strings.TrimSpace(agentName) == "" {
		return nil, fmt.Errorf("agentName is required")
	}
	q := url.Values{}
	if apiVersion != "" {
		q.Set("api-version", apiVersion)
	}

	req, err := runtime.NewRequest(ctx, http.MethodGet, c.agentsURL("/"+url.PathEscape(agentName), q))
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

// UpdateAgent replaces an existing managed agent's definition.
//
// POST {baseURL}{routePrefix}/agents/{name}?api-version=<apiVersion>
func (c *ManagedAgentClient) UpdateAgent(
	ctx context.Context,
	agentName string,
	request *UpdateAgentRequest,
	apiVersion string,
) (*AgentObject, error) {
	return c.UpdateAgentWithHeaders(ctx, agentName, request, apiVersion, nil)
}

// UpdateAgentWithHeaders replaces an existing managed agent's definition,
// publishing a new version, and forwards any additional headers (such as the
// x-model-endpoint routing hint) to the request. This is the prompt-agent
// re-deploy path: managed agents are versioned, so posting a new definition to
// an existing agent creates a new version rather than a conflict.
//
// POST {baseURL}{routePrefix}/agents/{name}?api-version=<apiVersion>
func (c *ManagedAgentClient) UpdateAgentWithHeaders(
	ctx context.Context,
	agentName string,
	request *UpdateAgentRequest,
	apiVersion string,
	headers map[string]string,
) (*AgentObject, error) {
	if strings.TrimSpace(agentName) == "" {
		return nil, fmt.Errorf("agentName is required")
	}
	q := url.Values{}
	if apiVersion != "" {
		q.Set("api-version", apiVersion)
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, c.agentsURL("/"+url.PathEscape(agentName), q))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	for k, v := range headers {
		req.Raw().Header.Set(k, v)
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

// DeleteAgent removes a managed agent. When force is true, the agent is
// deleted even if it has active sessions; when false the force query param
// is omitted entirely (matches the vienna harness default).
//
// DELETE {baseURL}{routePrefix}/agents/{name}?api-version=<apiVersion>[&force=true]
func (c *ManagedAgentClient) DeleteAgent(
	ctx context.Context,
	agentName, apiVersion string,
	force bool,
) (*DeleteAgentResponse, error) {
	if strings.TrimSpace(agentName) == "" {
		return nil, fmt.Errorf("agentName is required")
	}
	q := url.Values{}
	if apiVersion != "" {
		q.Set("api-version", apiVersion)
	}
	if force {
		q.Set("force", strconv.FormatBool(force))
	}

	req, err := runtime.NewRequest(ctx, http.MethodDelete, c.agentsURL("/"+url.PathEscape(agentName), q))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Accept both 200 (body) and 204 (no body) — vienna returns 204 in some configs.
	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusNoContent) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var deleteResponse DeleteAgentResponse
	if len(body) > 0 {
		if err := json.Unmarshal(body, &deleteResponse); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
	} else {
		deleteResponse = DeleteAgentResponse{Deleted: true, Name: agentName}
	}
	return &deleteResponse, nil
}

// ListAgents returns the managed agents in the workspace.
//
// GET {baseURL}{routePrefix}/agents?api-version=<apiVersion>[&kind=...&limit=...&after=...&before=...&order=...]
func (c *ManagedAgentClient) ListAgents(
	ctx context.Context,
	params *ListAgentQueryParameters,
	apiVersion string,
) (*AgentList, error) {
	q := url.Values{}
	if apiVersion != "" {
		q.Set("api-version", apiVersion)
	}
	if params != nil {
		if params.Kind != nil {
			q.Set("kind", string(*params.Kind))
		}
		if params.Limit != nil {
			q.Set("limit", strconv.Itoa(int(*params.Limit)))
		}
		if params.After != nil {
			q.Set("after", *params.After)
		}
		if params.Before != nil {
			q.Set("before", *params.Before)
		}
		if params.Order != nil {
			q.Set("order", *params.Order)
		}
	}

	req, err := runtime.NewRequest(ctx, http.MethodGet, c.agentsURL("", q))
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

	var list AgentList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &list, nil
}

// CreateResponse invokes a managed agent via the OpenAI-shape Responses API.
//
// POST {baseURL}{routePrefix}/openai/v1/responses
//
// The Responses surface is path-versioned (no api-version query). The target
// agent travels in the request body as
// `agent_reference: { type: "agent_reference", name: "<agent>" }`. The body
// is forwarded verbatim so callers can shape it as needed (input, model,
// tools, stream, etc.); the raw response body is returned so streaming
// (SSE) callers can scan it as it arrives.
func (c *ManagedAgentClient) CreateResponse(
	ctx context.Context,
	requestBody []byte,
	headers map[string]string,
) ([]byte, http.Header, error) {
	req, err := runtime.NewRequest(ctx, http.MethodPost, c.responsesURL(""))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	for k, v := range headers {
		req.Raw().Header.Set(k, v)
	}
	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(requestBody)), "application/json"); err != nil {
		return nil, nil, fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated, http.StatusAccepted) {
		return nil, nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return body, resp.Header.Clone(), nil
}

// CreateResponseStream is the streaming counterpart of CreateResponse. The
// raw HTTP response body is returned without being read so the caller can
// process the Server-Sent Events line-by-line as the harness emits them.
// The caller MUST close the returned body.
func (c *ManagedAgentClient) CreateResponseStream(
	ctx context.Context,
	requestBody []byte,
	headers map[string]string,
) (io.ReadCloser, http.Header, error) {
	req, err := runtime.NewRequest(ctx, http.MethodPost, c.responsesURL(""))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	for k, v := range headers {
		req.Raw().Header.Set(k, v)
	}
	// SSE responses are not buffered through azcore's body decoder — set
	// Accept so the server picks the streaming representation when given a
	// choice.
	if req.Raw().Header.Get("Accept") == "" {
		req.Raw().Header.Set("Accept", "text/event-stream")
	}
	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(requestBody)), "application/json"); err != nil {
		return nil, nil, fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated, http.StatusAccepted) {
		// On non-success the body usually has a JSON error payload; let
		// azcore parse it and close the body for us.
		return nil, nil, runtime.NewResponseError(resp)
	}
	return resp.Body, resp.Header.Clone(), nil
}

// GetResponse retrieves a previously created response by id.
//
// GET {baseURL}{routePrefix}/openai/v1/responses/{responseId}
func (c *ManagedAgentClient) GetResponse(
	ctx context.Context,
	responseID string,
) ([]byte, http.Header, error) {
	if strings.TrimSpace(responseID) == "" {
		return nil, nil, fmt.Errorf("responseID is required")
	}

	req, err := runtime.NewRequest(ctx, http.MethodGet, c.responsesURL("/"+url.PathEscape(responseID)))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return body, resp.Header.Clone(), nil
}

// CancelResponse cancels an in-flight response.
//
// POST {baseURL}{routePrefix}/openai/v1/responses/{responseId}/cancel
func (c *ManagedAgentClient) CancelResponse(
	ctx context.Context,
	responseID string,
) ([]byte, http.Header, error) {
	if strings.TrimSpace(responseID) == "" {
		return nil, nil, fmt.Errorf("responseID is required")
	}

	req, err := runtime.NewRequest(
		ctx, http.MethodPost, c.responsesURL("/"+url.PathEscape(responseID)+"/cancel"),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusAccepted) {
		return nil, nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return body, resp.Header.Clone(), nil
}

// DeleteResponse deletes a stored response.
//
// DELETE {baseURL}{routePrefix}/openai/v1/responses/{responseId}
func (c *ManagedAgentClient) DeleteResponse(
	ctx context.Context,
	responseID string,
) error {
	if strings.TrimSpace(responseID) == "" {
		return fmt.Errorf("responseID is required")
	}

	req, err := runtime.NewRequest(ctx, http.MethodDelete, c.responsesURL("/"+url.PathEscape(responseID)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

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

// BuildWorkspaceRoutePrefix is a convenience builder for the ARM-shaped route
// prefix expected by managed agent operations. Use it when constructing a
// ManagedAgentClient against a workspace identified by its
// subscription/resource-group/workspace tuple:
//
//	/agents/v2.0/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.MachineLearningServices/workspaces/<ws>
//
// All three arguments are required and must be non-empty.
func BuildWorkspaceRoutePrefix(subscriptionID, resourceGroup, workspace string) (string, error) {
	if strings.TrimSpace(subscriptionID) == "" {
		return "", fmt.Errorf("subscriptionID is required")
	}
	if strings.TrimSpace(resourceGroup) == "" {
		return "", fmt.Errorf("resourceGroup is required")
	}
	if strings.TrimSpace(workspace) == "" {
		return "", fmt.Errorf("workspace is required")
	}
	return fmt.Sprintf(
		"/agents/v2.0/subscriptions/%s/resourceGroups/%s/providers/Microsoft.MachineLearningServices/workspaces/%s",
		url.PathEscape(subscriptionID),
		url.PathEscape(resourceGroup),
		url.PathEscape(workspace),
	), nil
}

// SplitProjectEndpoint splits a Foundry project data-plane endpoint into the
// pieces a ManagedAgentClient needs. Given:
//
//	https://<account>.services.ai.azure.com/api/projects/<project>
//
// it returns BaseURL ("https://<account>.services.ai.azure.com") and
// RoutePrefix ("/api/projects/<project>"). The client then assembles the
// canonical managed agent routes off that prefix, e.g.:
//
//	{baseURL}{routePrefix}/agents
//	{baseURL}{routePrefix}/openai/responses
func SplitProjectEndpoint(projectEndpoint string) (baseURL, routePrefix string, err error) {
	pe := strings.TrimRight(strings.TrimSpace(projectEndpoint), "/")
	if pe == "" {
		return "", "", fmt.Errorf("projectEndpoint is required")
	}
	u, err := url.Parse(pe)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("projectEndpoint %q is not a valid absolute URL", projectEndpoint)
	}
	routePrefix = strings.TrimRight(u.Path, "/")
	if routePrefix == "" {
		return "", "", fmt.Errorf("projectEndpoint %q is missing the /api/projects/<project> path", projectEndpoint)
	}
	return u.Scheme + "://" + u.Host, routePrefix, nil
}
