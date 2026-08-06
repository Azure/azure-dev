// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package gen_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"

	"azureaidataset/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

const (
	pathDataGenerationJobs = "/data_generation_jobs"
	pathAgents             = "/agents"
)

// Client talks to the evaluation service's data-generation routes.
//
// Datasets are registered through the dataset API, but they are *generated* by
// the evaluation service, so this extension speaks to both.
type Client struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewClient creates a Client for the given project endpoint.
func NewClient(endpoint string, cred azcore.TokenCredential) *Client {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-dataset/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{"X-Ms-Correlation-Request-Id", "X-Request-Id"},
			IncludeBody:    false,
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(userAgent),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-dataset",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &Client{endpoint: endpoint, pipeline: pipeline}
}

// NewClientFromPipeline creates a Client with a pre-built pipeline, for tests
// that need to bypass auth policies.
func NewClientFromPipeline(endpoint string, pipeline runtime.Pipeline) *Client {
	return &Client{endpoint: endpoint, pipeline: pipeline}
}

// CreateDataGenerationJob starts a dataset generation job.
func (c *Client) CreateDataGenerationJob(
	ctx context.Context,
	request *DataGenerationJobRequest,
	apiVersion string,
) (*GenerationJob, error) {
	return doRequestTyped[GenerationJob](
		c, ctx, http.MethodPost, pathDataGenerationJobs, request, apiVersion)
}

// GetDataGenerationJob gets the current state of a dataset generation job.
func (c *Client) GetDataGenerationJob(
	ctx context.Context,
	operationID string,
	apiVersion string,
) (*GenerationJob, error) {
	path := pathDataGenerationJobs + "/" + url.PathEscape(operationID)
	return doRequestTyped[GenerationJob](c, ctx, http.MethodGet, path, nil, apiVersion)
}

// ListDataGenerationJobs returns the project's dataset generation jobs.
func (c *Client) ListDataGenerationJobs(
	ctx context.Context,
	apiVersion string,
) (*GenerationJobList, error) {
	return doRequestTyped[GenerationJobList](
		c, ctx, http.MethodGet, pathDataGenerationJobs, nil, apiVersion)
}

// CancelDataGenerationJob stops a dataset generation job.
//
// The separator is a colon, not a path segment: `{id}/cancel` is a 404 while
// `{id}:cancel` reaches the action. The empty object is what carries a content
// type, without which the route answers 415.
func (c *Client) CancelDataGenerationJob(
	ctx context.Context,
	operationID string,
	apiVersion string,
) (*GenerationJob, error) {
	path := pathDataGenerationJobs + "/" + url.PathEscape(operationID) + ":cancel"
	return doRequestTyped[GenerationJob](
		c, ctx, http.MethodPost, path, json.RawMessage(`{}`), apiVersion)
}

// DeleteDataGenerationJob discards the job record. The dataset the job produced
// is already registered and is not affected.
func (c *Client) DeleteDataGenerationJob(
	ctx context.Context,
	operationID string,
	apiVersion string,
) error {
	path := pathDataGenerationJobs + "/" + url.PathEscape(operationID)
	_, err := c.doRequest(ctx, http.MethodDelete, path, nil, apiVersion)
	return err
}

// GetAgent reads an agent from the project's catalog.
//
// Only the newest version is returned, which is the one generation is seeded
// from: the point is to describe what the agent does now.
func (c *Client) GetAgent(
	ctx context.Context,
	name string,
	apiVersion string,
) (*Agent, error) {
	path := pathAgents + "/" + url.PathEscape(name)
	return doRequestTyped[Agent](c, ctx, http.MethodGet, path, nil, apiVersion)
}

func (c *Client) doRequest(
	ctx context.Context,
	method string,
	path string,
	body any,
	apiVersion string,
) ([]byte, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	// Callers escape the ids they interpolate, so the path is set as the raw
	// one. Assigning it to u.Path re-escapes the percent signs, and a job id
	// carrying a separator then addresses a literally-named resource.
	escapedPath := u.EscapedPath() + path
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return nil, fmt.Errorf("invalid request path %q: %w", escapedPath, err)
	}
	u.Path, u.RawPath = decodedPath, escapedPath

	q := u.Query()
	if apiVersion != "" {
		q.Set("api-version", apiVersion)
	}
	u.RawQuery = q.Encode()

	req, err := runtime.NewRequest(ctx, method, u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	log.Printf("[gen_api] %s %s", method, u.Redacted())

	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
			return nil, fmt.Errorf("failed to set request body: %w", err)
		}
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	log.Printf("[gen_api] response status: %d", resp.StatusCode)

	// 204 belongs here: a delete that removed the resource answers No Content,
	// and treating that as a failure reports every successful delete as an
	// error.
	if !runtime.HasStatusCode(resp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent) {
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil, runtime.NewResponseError(resp)
	}

	return respBody, nil
}

func doRequestTyped[T any](
	c *Client,
	ctx context.Context,
	method string,
	path string,
	body any,
	apiVersion string,
) (*T, error) {
	respBody, err := c.doRequest(ctx, method, path, body, apiVersion)
	if err != nil {
		return nil, err
	}

	if len(respBody) == 0 {
		return new(T), nil
	}

	var result T
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}
