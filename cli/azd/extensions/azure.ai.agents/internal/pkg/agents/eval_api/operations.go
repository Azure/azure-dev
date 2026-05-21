// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"azureaiagent/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// API path prefixes for eval service endpoints.
const (
	pathDataGenerationJobs      = "/data_generation_jobs"
	pathEvaluatorGenerationJobs = "/evaluator_generation_jobs"
	pathEvaluators              = "/evaluators"
	pathDatasets                = "/datasets"
	pathOpenAIEvals             = "/openai/evals"
)

// EvalClient provides methods for interacting with the Azure AI eval APIs.
type EvalClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewEvalClient creates a new EvalClient.
func NewEvalClient(endpoint string, cred azcore.TokenCredential) *EvalClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

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
		"azure-ai-evals",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &EvalClient{
		endpoint: endpoint,
		pipeline: pipeline,
	}
}

// NewEvalClientFromPipeline creates an EvalClient with a pre-built pipeline.
// This is intended for tests that need to bypass auth policies.
func NewEvalClientFromPipeline(endpoint string, pipeline runtime.Pipeline) *EvalClient {
	return &EvalClient{
		endpoint: endpoint,
		pipeline: pipeline,
	}
}

// CreateDataGenerationJob starts a dataset generation job for eval onboarding.
func (c *EvalClient) CreateDataGenerationJob(
	ctx context.Context,
	request *DataGenerationJobRequest,
	apiVersion string,
) (*GenerationJob, error) {
	return doRequestTyped[GenerationJob](c, ctx, http.MethodPost, pathDataGenerationJobs, nil, request, apiVersion)
}

// GetDataGenerationJob gets the current state of a dataset generation job.
func (c *EvalClient) GetDataGenerationJob(
	ctx context.Context,
	operationID string,
	apiVersion string,
) (*GenerationJob, error) {
	path := pathDataGenerationJobs + "/" + url.PathEscape(operationID)
	return doRequestTyped[GenerationJob](c, ctx, http.MethodGet, path, nil, nil, apiVersion)
}

// CreateEvaluatorGenerationJob starts an evaluator generation job for eval onboarding.
func (c *EvalClient) CreateEvaluatorGenerationJob(
	ctx context.Context,
	request *EvaluatorGenerationJobRequest,
	apiVersion string,
) (*GenerationJob, error) {
	return doRequestTyped[GenerationJob](c, ctx, http.MethodPost, pathEvaluatorGenerationJobs, nil, request, apiVersion)
}

// GetEvaluatorGenerationJob gets the current state of an evaluator generation job.
func (c *EvalClient) GetEvaluatorGenerationJob(
	ctx context.Context,
	operationID string,
	apiVersion string,
) (*GenerationJob, error) {
	path := pathEvaluatorGenerationJobs + "/" + url.PathEscape(operationID)
	return doRequestTyped[GenerationJob](c, ctx, http.MethodGet, path, nil, nil, apiVersion)
}

// CreateEvaluatorVersion creates a new version of a named evaluator.
// The body should be the full evaluator JSON with the definition field updated.
func (c *EvalClient) CreateEvaluatorVersion(
	ctx context.Context,
	name string,
	body json.RawMessage,
	apiVersion string,
) (*EvaluatorVersion, error) {
	path := pathEvaluators + "/" + url.PathEscape(name) + "/versions"
	return doRequestTyped[EvaluatorVersion](c, ctx, http.MethodPost, path, nil, body, apiVersion)
}

// GetEvaluatorRaw gets an evaluator by name and version as raw JSON.
// If version is empty, the latest version is fetched.
func (c *EvalClient) GetEvaluatorRaw(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (json.RawMessage, error) {
	path := pathEvaluators + "/" + url.PathEscape(name)
	if version != "" {
		path += "/versions/" + url.PathEscape(version)
	}
	return c.doRequest(ctx, http.MethodGet, path, nil, nil, apiVersion)
}

// CreateOpenAIEval creates an OpenAI eval definition.
func (c *EvalClient) CreateOpenAIEval(
	ctx context.Context,
	request *CreateOpenAIEvalRequest,
	apiVersion string,
) (*OpenAIEval, error) {
	return doRequestTyped[OpenAIEval](c, ctx, http.MethodPost, pathOpenAIEvals, nil, request, apiVersion)
}

// ListOpenAIEvals lists OpenAI eval definitions.
func (c *EvalClient) ListOpenAIEvals(ctx context.Context, limit int, apiVersion string) (*OpenAIEvalList, error) {
	query := map[string]string{}
	if limit > 0 {
		query["limit"] = strconv.Itoa(limit)
	}

	return doRequestTyped[OpenAIEvalList](c, ctx, http.MethodGet, pathOpenAIEvals, query, nil, apiVersion)
}

// GetOpenAIEval gets an OpenAI eval definition.
func (c *EvalClient) GetOpenAIEval(ctx context.Context, evalID string, apiVersion string) (*OpenAIEval, error) {
	path := pathOpenAIEvals + "/" + url.PathEscape(evalID)
	return doRequestTyped[OpenAIEval](c, ctx, http.MethodGet, path, nil, nil, apiVersion)
}

// CreateOpenAIEvalRun starts a run for an OpenAI eval definition.
func (c *EvalClient) CreateOpenAIEvalRun(
	ctx context.Context,
	evalID string,
	request *CreateOpenAIEvalRunRequest,
	apiVersion string,
) (*OpenAIEvalRun, error) {
	path := fmt.Sprintf("%s/%s/runs", pathOpenAIEvals, url.PathEscape(evalID))
	return doRequestTyped[OpenAIEvalRun](c, ctx, http.MethodPost, path, nil, request, apiVersion)
}

// ListOpenAIEvalRuns lists runs for an OpenAI eval definition.
func (c *EvalClient) ListOpenAIEvalRuns(
	ctx context.Context,
	evalID string,
	limit int,
	apiVersion string,
) (*OpenAIEvalRunList, error) {
	query := map[string]string{}
	if limit > 0 {
		query["limit"] = strconv.Itoa(limit)
	}

	path := fmt.Sprintf("%s/%s/runs", pathOpenAIEvals, url.PathEscape(evalID))
	return doRequestTyped[OpenAIEvalRunList](c, ctx, http.MethodGet, path, query, nil, apiVersion)
}

// GetOpenAIEvalRun gets a run for an OpenAI eval definition.
func (c *EvalClient) GetOpenAIEvalRun(
	ctx context.Context,
	evalID string,
	runID string,
	apiVersion string,
) (*OpenAIEvalRun, error) {
	path := fmt.Sprintf("%s/%s/runs/%s", pathOpenAIEvals, url.PathEscape(evalID), url.PathEscape(runID))
	return doRequestTyped[OpenAIEvalRun](c, ctx, http.MethodGet, path, nil, nil, apiVersion)
}

func (c *EvalClient) doRequest(
	ctx context.Context,
	method string,
	path string,
	query map[string]string,
	body any,
	apiVersion string,
) ([]byte, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += path
	q := u.Query()
	if apiVersion != "" {
		q.Set("api-version", apiVersion)
	}
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	req, err := runtime.NewRequest(ctx, method, u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	log.Printf("[eval_api] %s %s", method, u.Redacted())

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

	log.Printf("[eval_api] response status: %d", resp.StatusCode)

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated, http.StatusAccepted) {
		// Restore the body so runtime.NewResponseError can read it.
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil, runtime.NewResponseError(resp)
	}

	return respBody, nil
}

// doRequestTyped performs an HTTP request and unmarshals the response into T.
func doRequestTyped[T any](
	c *EvalClient,
	ctx context.Context,
	method string,
	path string,
	query map[string]string,
	body any,
	apiVersion string,
) (*T, error) {
	respBody, err := c.doRequest(ctx, method, path, query, body, apiVersion)
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
