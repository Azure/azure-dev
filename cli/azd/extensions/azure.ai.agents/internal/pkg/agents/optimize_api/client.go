// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package optimize_api provides an HTTP client for the agent optimization
// service API. It supports job submission, status polling, cancellation,
// and candidate config/file retrieval.
package optimize_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	netURL "net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"

	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"

	"azureaiagent/internal/pkg/useragent"
)

// OptimizeClient provides methods for interacting with the Agents Optimization API.
type OptimizeClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// PromotionReportOnlyHeader tells the promote endpoint to record an existing
// agent version without creating another version.
const PromotionReportOnlyHeader = "x-ms-optimization-agent-promotion-report-only"

// NewOptimizeClient creates a new OptimizeClient with the given endpoint and credential.
func NewOptimizeClient(endpoint string, cred azcore.TokenCredential) *OptimizeClient {
	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{"X-Ms-Correlation-Request-Id", "X-Request-Id"},
			IncludeBody:    false,
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(useragent.Default()),
		},
	}

	pipeline := runtime.NewPipeline(
		"agents-optimization",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &OptimizeClient{
		endpoint: endpoint,
		pipeline: pipeline,
	}
}

// NewOptimizeClientFromPipeline creates an OptimizeClient with a pre-built pipeline.
// This is intended for tests that need to bypass auth policies.
func NewOptimizeClientFromPipeline(endpoint string, pipeline runtime.Pipeline) *OptimizeClient {
	return &OptimizeClient{
		endpoint: endpoint,
		pipeline: pipeline,
	}
}

// StartOptimize submits a new optimization job. Request semantics are validated by the service.
func (c *OptimizeClient) StartOptimize(
	ctx context.Context,
	optimizeReq *OptimizeRequest,
) (*OptimizeResponse, error) {
	url := fmt.Sprintf("%s/%s?api-version=%s", c.endpoint, optimizeJobsPath, APIVersion)

	payload, err := json.Marshal(optimizeReq)
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

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated, http.StatusAccepted) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var result OptimizeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if result.ID == "" {
		return nil, fmt.Errorf("optimization create response is missing id")
	}
	result.raw = body

	return &result, nil
}

// GetOptimizeStatus retrieves the status of an optimization job.
func (c *OptimizeClient) GetOptimizeStatus(
	ctx context.Context,
	operationID string,
) (*OptimizeJobStatus, error) {
	url := fmt.Sprintf("%s/%s/%s?api-version=%s", c.endpoint, optimizeJobsPath, netURL.PathEscape(operationID), APIVersion)

	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusAccepted) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var result OptimizeJobStatus
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	result.raw = body

	return &result, nil
}

// ListOptimizeJobs lists optimization jobs with optional filtering.
func (c *OptimizeClient) ListOptimizeJobs(
	ctx context.Context,
	limit int,
	status string,
) (*OptimizeListResponse, error) {
	url := fmt.Sprintf("%s/%s?api-version=%s&limit=%d", c.endpoint, optimizeJobsPath, APIVersion, limit)
	if status != "" {
		url += "&status=" + netURL.QueryEscape(status)
	}

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

	var result OptimizeListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	result.raw = body
	var page struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("failed to preserve job response: %w", err)
	}
	for i := range result.Data {
		result.Data[i].raw = page.Data[i]
	}

	return &result, nil
}

// CancelOptimize cancels a running optimization job.
func (c *OptimizeClient) CancelOptimize(
	ctx context.Context,
	operationID string,
) (*OptimizeCancelResponse, error) {
	url := fmt.Sprintf("%s/%s/%s:cancel?api-version=%s", c.endpoint, optimizeJobsPath, netURL.PathEscape(operationID), APIVersion)

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
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

	var result OptimizeCancelResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	result.raw = body

	return &result, nil
}

// ReportDeployment notifies the optimization service that a candidate has been
// deployed. This allows the optimization service to track which candidates have been deployed.
func (c *OptimizeClient) ReportDeployment(
	ctx context.Context,
	jobID string,
	report *DeploymentReport,
) error {
	return c.ReportDeploymentWithHeaders(ctx, jobID, report, nil)
}

// ReportDeploymentWithHeaders reports a deployment with additional service headers.
func (c *OptimizeClient) ReportDeploymentWithHeaders(
	ctx context.Context,
	jobID string,
	report *DeploymentReport,
	headers map[string]string,
) error {
	url := fmt.Sprintf(
		"%s/%s/%s/candidates/%s:promote?api-version=%s",
		c.endpoint, optimizeJobsPath, netURL.PathEscape(jobID), netURL.PathEscape(report.CandidateID), APIVersion,
	)

	payload, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal deployment report: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	for key, value := range headers {
		req.Raw().Header.Set(key, value)
	}

	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(payload)), "application/json",
	); err != nil {
		return fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent) {
		return runtime.NewResponseError(resp)
	}

	return nil
}

// GetCandidateConfig resolves a complete candidate configuration for apply and deploy.
// Missing fields come from the job's submitted baseline; candidate fields and mutations take precedence.
func (c *OptimizeClient) GetCandidateConfig(
	ctx context.Context,
	jobID string,
	candidateID string,
) (json.RawMessage, error) {
	candidate, err := c.GetCandidate(ctx, jobID, candidateID)
	if err != nil {
		return nil, err
	}
	for _, file := range candidate.Files {
		if file.Type != "config" {
			continue
		}
		body, err := c.GetCandidateFile(ctx, jobID, candidateID, file.Path)
		if err != nil {
			return nil, fmt.Errorf("downloading candidate config: %w", err)
		}
		if !json.Valid([]byte(body)) {
			return nil, fmt.Errorf("candidate config is not valid JSON")
		}
		job, err := c.GetOptimizeStatus(ctx, jobID)
		if err != nil {
			return nil, fmt.Errorf("loading candidate baseline from optimization job: %w", err)
		}
		var snapshot struct {
			Configuration struct {
				Baseline map[string]json.RawMessage `json:"baseline_agent_configuration"`
			} `json:"optimization_configuration"`
		}
		if err := json.Unmarshal(job.raw, &snapshot); err != nil {
			return nil, fmt.Errorf("parsing optimization job baseline: %w", err)
		}
		return completeCandidateConfig(json.RawMessage(body), snapshot.Configuration.Baseline, candidate.Output)
	}
	return nil, fmt.Errorf("candidate %q has no published config file", candidateID)
}

func completeCandidateConfig(
	raw json.RawMessage,
	baseline map[string]json.RawMessage,
	output *CandidateOutput,
) (json.RawMessage, error) {
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("parsing candidate config: %w", err)
	}
	if config == nil {
		return nil, fmt.Errorf("candidate config must be a JSON object")
	}

	for _, field := range []struct{ name, baseline string }{
		{"instructions", "system_prompt"},
		{"model", "current_model"},
		{"skills", "skills"},
		{"tools", "tools"},
	} {
		if _, exists := config[field.name]; exists {
			continue
		}
		if field.name == "instructions" {
			if _, exists := config["system_prompt"]; exists {
				continue
			}
			if _, exists := config["systemPrompt"]; exists {
				continue
			}
		}
		if value, exists := baseline[field.baseline]; exists {
			config[field.name] = value
		}
	}

	if output != nil {
		for _, mutation := range output.Mutations {
			switch mutation.Type {
			case "instructions":
				delete(config, "system_prompt")
				delete(config, "systemPrompt")
			case "model", "skills", "tools":
			default:
				return nil, fmt.Errorf("unsupported candidate mutation %q", mutation.Type)
			}
			if len(mutation.Value) == 0 {
				return nil, fmt.Errorf("candidate mutation %q is missing its value", mutation.Type)
			}
			config[mutation.Type] = mutation.Value
		}
	}

	for _, name := range []string{"skills", "tools"} {
		if value, exists := config[name]; exists {
			var items []json.RawMessage
			if err := json.Unmarshal(value, &items); err != nil || items == nil {
				return nil, fmt.Errorf("candidate config %s must be an array", name)
			}
		}
	}
	for _, name := range []string{"model", "instructions", "system_prompt", "systemPrompt"} {
		if value, exists := config[name]; exists {
			var text *string
			if err := json.Unmarshal(value, &text); err != nil || text == nil {
				return nil, fmt.Errorf("candidate config %s must be a string", name)
			}
		}
	}

	resolved, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("serializing candidate config: %w", err)
	}
	return resolved, nil
}

// GetCandidate fetches the candidate manifest (metadata + file list) from the optimization service.
// GET /agent_optimization_jobs/{jobId}/candidates/{id}
func (c *OptimizeClient) GetCandidate(
	ctx context.Context,
	jobID string,
	candidateID string,
) (*CandidateManifest, error) {
	url := fmt.Sprintf("%s/%s/%s/candidates/%s?api-version=%s",
		c.endpoint, optimizeJobsPath, netURL.PathEscape(jobID), netURL.PathEscape(candidateID), APIVersion)

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

	var manifest CandidateManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse candidate manifest: %w", err)
	}
	return &manifest, nil
}

// GetCandidateFile downloads a single file from a candidate.
// GET /agent_optimization_jobs/{jobId}/files?candidate_id={id}&path={path}
func (c *OptimizeClient) GetCandidateFile(
	ctx context.Context,
	jobID string,
	candidateID string,
	filePath string,
) (string, error) {
	url := fmt.Sprintf("%s/%s/%s/files?api-version=%s&candidate_id=%s&path=%s",
		c.endpoint, optimizeJobsPath, netURL.PathEscape(jobID),
		APIVersion, netURL.QueryEscape(candidateID),
		netURL.QueryEscape(filePath))

	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return "", runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(body), nil
}

// LoadCandidates loads every candidate page for job rendering in stable creation order.
func (c *OptimizeClient) LoadCandidates(ctx context.Context, job *OptimizeJobStatus) error {
	var candidates []CandidateResult
	after := ""
	seen := make(map[string]bool)
	for {
		url := fmt.Sprintf("%s/%s/%s/candidates?api-version=%s&limit=100&order=asc",
			c.endpoint, optimizeJobsPath, netURL.PathEscape(job.ID), APIVersion)
		if after != "" {
			url += "&after=" + netURL.QueryEscape(after)
		}
		req, err := runtime.NewRequest(ctx, http.MethodGet, url)
		if err != nil {
			return fmt.Errorf("creating candidates request: %w", err)
		}
		resp, err := c.pipeline.Do(req)
		if err != nil {
			return fmt.Errorf("listing candidates: %w", err)
		}
		if !runtime.HasStatusCode(resp, http.StatusOK) {
			err := runtime.NewResponseError(resp)
			_ = resp.Body.Close()
			return err
		}
		var page CandidateListResponse
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("decoding candidates: %w", err)
		}
		candidates = append(candidates, page.Data...)
		if !page.HasMore {
			break
		}
		if page.LastID == "" || seen[page.LastID] {
			return fmt.Errorf("candidate pagination did not advance")
		}
		seen[page.LastID] = true
		after = page.LastID
	}
	job.CandidateResults = candidates
	return nil
}
