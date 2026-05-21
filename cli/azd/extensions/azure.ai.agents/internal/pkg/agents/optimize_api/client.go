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

	"azureaiagent/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// OptimizeClient provides methods for interacting with the Agents Optimization API.
type OptimizeClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewOptimizeClient creates a new OptimizeClient with the given endpoint and credential.
func NewOptimizeClient(endpoint string, cred azcore.TokenCredential) *OptimizeClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{"X-Ms-Correlation-Request-Id", "X-Request-Id"},
			IncludeBody:    true,
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(userAgent),
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

// StartOptimize submits a new optimization job.
func (c *OptimizeClient) StartOptimize(
	ctx context.Context,
	optimizeReq *OptimizeRequest,
) (*OptimizeResponse, error) {
	url := fmt.Sprintf("%s/optimize?api-version=v1", c.endpoint)

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

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusAccepted) {
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

	return &result, nil
}

// GetOptimizeStatus retrieves the status of an optimization job.
func (c *OptimizeClient) GetOptimizeStatus(
	ctx context.Context,
	operationID string,
) (*OptimizeJobStatus, error) {
	url := fmt.Sprintf("%s/optimize/%s?api-version=v1", c.endpoint, operationID)

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

	var result OptimizeJobStatus
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// ListOptimizeJobs lists optimization jobs with optional filtering.
func (c *OptimizeClient) ListOptimizeJobs(
	ctx context.Context,
	limit int,
	status string,
) (*OptimizeListResponse, error) {
	url := fmt.Sprintf("%s/optimize?api-version=v1&limit=%d", c.endpoint, limit)
	if status != "" {
		url += "&status=" + status
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

	return &result, nil
}

// CancelOptimize cancels a running optimization job.
func (c *OptimizeClient) CancelOptimize(
	ctx context.Context,
	operationID string,
) (*OptimizeCancelResponse, error) {
	url := fmt.Sprintf("%s/optimize/%s/cancel?api-version=v1", c.endpoint, operationID)

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

	return &result, nil
}

// ReportDeployment notifies the optimization service that a candidate has been
// deployed. This allows FAOS to track which candidates have been deployed.
func (c *OptimizeClient) ReportDeployment(
	ctx context.Context,
	report *DeploymentReport,
) error {
	url := fmt.Sprintf(
		"%s/optimize/candidates/%s:promote?api-version=v1",
		c.endpoint, report.CandidateID,
	)

	payload, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal deployment report: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
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

// GetCandidateConfig fetches the candidate configuration from the optimization service.
// GET /optimize/candidates/{id}/config
func (c *OptimizeClient) GetCandidateConfig(
	ctx context.Context,
	candidateID string,
) (any, error) {
	url := fmt.Sprintf("%s/optimize/candidates/%s/config?api-version=v1", c.endpoint, candidateID)

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

	var config any
	if err := json.Unmarshal(body, &config); err != nil {
		return nil, fmt.Errorf("failed to parse candidate config: %w", err)
	}
	return config, nil
}

// GetCandidate fetches the candidate manifest (metadata + file list) from FAOS.
// GET /optimize/candidates/{id}
func (c *OptimizeClient) GetCandidate(
	ctx context.Context,
	candidateID string,
) (*CandidateManifest, error) {
	url := fmt.Sprintf("%s/optimize/candidates/%s?api-version=v1", c.endpoint, candidateID)

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
// GET /optimize/candidates/{id}/files?path={path}
func (c *OptimizeClient) GetCandidateFile(
	ctx context.Context,
	candidateID string,
	filePath string,
) (string, error) {
	url := fmt.Sprintf("%s/optimize/candidates/%s/files?api-version=v1&path=%s",
		c.endpoint, candidateID, netURL.QueryEscape(filePath))

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
