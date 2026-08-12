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
	"time"

	"azureaieval/internal/messages"
	"azureaieval/internal/version"

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
	pathOpenAIEvals             = "/openai/v1/evals"
	pathAgents                  = "/agents"
)

// EvalClient provides methods for interacting with the Azure AI eval APIs.
type EvalClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewEvalClient creates a new EvalClient.
func NewEvalClient(endpoint string, cred azcore.TokenCredential) *EvalClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-evaluations/%s", version.Version)

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

// GenerationJobList is the listing envelope both job types answer with. It is
// `data`, not the `value` the dataset and evaluator routes use.
type GenerationJobList struct {
	Data []GenerationJob `json:"data"`
}

// ListDataGenerationJobs returns the project's dataset generation jobs.
func (c *EvalClient) ListDataGenerationJobs(
	ctx context.Context,
	apiVersion string,
) (*GenerationJobList, error) {
	return doRequestTyped[GenerationJobList](
		c, ctx, http.MethodGet, pathDataGenerationJobs, nil, nil, apiVersion)
}

// ListEvaluatorGenerationJobs returns the project's evaluator generation jobs.
func (c *EvalClient) ListEvaluatorGenerationJobs(
	ctx context.Context,
	apiVersion string,
) (*GenerationJobList, error) {
	return doRequestTyped[GenerationJobList](
		c, ctx, http.MethodGet, pathEvaluatorGenerationJobs, nil, nil, apiVersion)
}

// CancelDataGenerationJob stops a dataset generation job.
func (c *EvalClient) CancelDataGenerationJob(
	ctx context.Context,
	operationID string,
	apiVersion string,
) (*GenerationJob, error) {
	return c.cancelGenerationJob(ctx, pathDataGenerationJobs, operationID, apiVersion)
}

// CancelEvaluatorGenerationJob stops an evaluator generation job.
func (c *EvalClient) CancelEvaluatorGenerationJob(
	ctx context.Context,
	operationID string,
	apiVersion string,
) (*GenerationJob, error) {
	return c.cancelGenerationJob(ctx, pathEvaluatorGenerationJobs, operationID, apiVersion)
}

// cancelGenerationJob posts to the colon form of the route.
//
// The separator is a colon, not a path segment: `{id}/cancel` is a 404 while
// `{id}:cancel` reaches the action. The empty object is what carries a content
// type, without which the route answers 415.
func (c *EvalClient) cancelGenerationJob(
	ctx context.Context,
	basePath, operationID, apiVersion string,
) (*GenerationJob, error) {
	path := basePath + "/" + url.PathEscape(operationID) + ":cancel"
	return doRequestTyped[GenerationJob](
		c, ctx, http.MethodPost, path, nil, json.RawMessage(`{}`), apiVersion)
}

// DeleteDataGenerationJob removes a dataset generation job record.
func (c *EvalClient) DeleteDataGenerationJob(
	ctx context.Context,
	operationID string,
	apiVersion string,
) error {
	return c.deleteGenerationJob(ctx, pathDataGenerationJobs, operationID, apiVersion)
}

// DeleteEvaluatorGenerationJob removes an evaluator generation job record.
func (c *EvalClient) DeleteEvaluatorGenerationJob(
	ctx context.Context,
	operationID string,
	apiVersion string,
) error {
	return c.deleteGenerationJob(ctx, pathEvaluatorGenerationJobs, operationID, apiVersion)
}

// deleteGenerationJob discards the job record. The artifact the job produced is
// already registered and is not affected.
func (c *EvalClient) deleteGenerationJob(
	ctx context.Context,
	basePath, operationID, apiVersion string,
) error {
	path := basePath + "/" + url.PathEscape(operationID)
	_, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil, apiVersion)
	return err
}

// GetAgent reads an agent from the project's catalog.
//
// Only the newest version is returned, which is the one generation is seeded
// from: the point is to describe what the agent does now.
func (c *EvalClient) GetAgent(
	ctx context.Context,
	name string,
	apiVersion string,
) (*Agent, error) {
	path := pathAgents + "/" + url.PathEscape(name)
	return doRequestTyped[Agent](c, ctx, http.MethodGet, path, nil, nil, apiVersion)
}

// CreateEvaluatorVersion creates a new version of a named evaluator.
// The body should be the full evaluator JSON with the definition field updated.
//
// previous is the evaluator document the caller has already read, or nil when
// it read none. It is what keeps the publish from being answered with the
// version that document holds.
func (c *EvalClient) CreateEvaluatorVersion(
	ctx context.Context,
	name string,
	body json.RawMessage,
	previous json.RawMessage,
	apiVersion string,
) (*EvaluatorVersion, error) {
	return c.publishEvaluatorVersion(ctx, name, previous, apiVersion, func() (*EvaluatorVersion, error) {
		path := pathEvaluators + "/" + url.PathEscape(name) + "/versions"
		return doRequestTyped[EvaluatorVersion](c, ctx, http.MethodPost, path, nil, body, apiVersion)
	})
}

// versionSettle bounds the wait for the service to start assigning the next
// version number.
const (
	versionSettleTimeout  = 45 * time.Second
	versionSettleInterval = 3 * time.Second
	versionSettleAge      = 8 * time.Second
)

// publishedVersion is the little of an evaluator document this needs: which
// version it is, and when it was written.
type publishedVersion struct {
	Version    string    `json:"version"`
	ModifiedAt time.Time `json:"modified_at"`
	CreatedAt  time.Time `json:"created_at"`
}

// writtenAt reports when the version was last written, preferring the
// modification time and falling back to creation.
func (p publishedVersion) writtenAt() time.Time {
	if !p.ModifiedAt.IsZero() {
		return p.ModifiedAt
	}
	return p.CreatedAt
}

// publishEvaluatorVersion publishes and then makes sure a new version is what
// came back.
//
// For a few seconds after a publish the service can answer the next one with
// the version it just assigned, writing over that version's contents instead
// of adding one. It is a race rather than a fixed window — a second publish
// has been seen both colliding a quarter of a second later and succeeding
// immediately — and nothing observable marks its end.
//
// That matters because versions are the unit an eval binds to. `evaluator
// create` followed by `evaluator update`, which is what a first authoring
// session looks like, would otherwise leave one version holding the second
// definition and every eval bound to the first silently scoring against a
// rubric nobody chose.
//
// So there are two defenses. The publish is held back until the version the
// caller read has had time to settle, which is what keeps the collision from
// happening at all; and the version that comes back is checked, which is what
// keeps a collision that happens anyway from being reported as success. The
// recheck republishes the same body, so it cannot make a collision worse than
// the first attempt already did.
//
// What the caller reads is used rather than the version listing because the
// listing lags a publish too: asked immediately after a create it answers 404,
// so a guard that trusted it would stand down in exactly the case it exists
// for. Callers that publish an evaluator have already read it to decide
// between creating and updating.
func (c *EvalClient) publishEvaluatorVersion(
	ctx context.Context,
	name string,
	previous json.RawMessage,
	apiVersion string,
	publish func() (*EvaluatorVersion, error),
) (*EvaluatorVersion, error) {
	var known publishedVersion
	if len(previous) > 0 {
		_ = json.Unmarshal(previous, &known)
	}

	latest := parseVersionNumber(known.Version)
	if listed := c.LatestEvaluatorVersionNumber(ctx, name, apiVersion); listed > latest {
		latest = listed
	}

	if written := known.writtenAt(); !written.IsZero() {
		if wait := versionSettleAge - time.Since(written); wait > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
	}

	deadline := time.Now().Add(versionSettleTimeout)
	for {
		created, err := publish()
		if err != nil {
			return nil, err
		}
		if latest == 0 || parseVersionNumber(created.Version) > latest {
			return created, nil
		}
		if time.Now().After(deadline) {
			return nil, messages.EvaluatorVersionNotAdvancing(
				name, created.Version, versionSettleTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(versionSettleInterval):
		}
	}
}

// GetEvaluatorRaw gets an evaluator by name and version as raw JSON.
// If version is empty, the latest version is resolved first.
//
// The service has no route for an unversioned evaluator: GET
// /evaluators/{name} returns 404 with no body, so the version cannot simply be
// left off the path.
func (c *EvalClient) GetEvaluatorRaw(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (json.RawMessage, error) {
	if version == "" {
		latest, err := c.LatestEvaluatorVersion(ctx, name, apiVersion)
		if err != nil {
			return nil, err
		}
		version = latest
	}
	path := pathEvaluators + "/" + url.PathEscape(name) +
		"/versions/" + url.PathEscape(version)
	return c.doRequest(ctx, http.MethodGet, path, nil, nil, apiVersion)
}

// LatestEvaluatorVersion returns the newest registered version of an evaluator.
func (c *EvalClient) LatestEvaluatorVersion(
	ctx context.Context,
	name string,
	apiVersion string,
) (string, error) {
	list, err := c.ListEvaluatorVersions(ctx, name, apiVersion)
	if err != nil {
		return "", err
	}
	if list == nil || len(list.Value) == 0 {
		return "", messages.EvaluatorHasNoVersions(name)
	}
	latest := pickLatestVersion(list.Value)
	if latest == "" {
		return "", messages.EvaluatorHasNoUsableVersion(name)
	}
	return latest, nil
}

// pickLatestVersion selects the highest evaluator version.
//
// Versions are integers rendered as strings, so they are compared numerically:
// a lexical compare would rank "9" above "15", and the service already
// publishes evaluators at version 15 and 17. A non-numeric version is used
// only when nothing numeric is present.
func pickLatestVersion(entries []EvaluatorSummary) string {
	best := ""
	bestNum := -1
	for _, entry := range entries {
		if entry.Version == "" {
			continue
		}
		num, err := strconv.Atoi(entry.Version)
		if err != nil {
			if best == "" {
				best = entry.Version
			}
			continue
		}
		if num > bestNum {
			bestNum, best = num, entry.Version
		}
	}
	return best
}

// CreateOpenAIEval creates an OpenAI eval definition.
func (c *EvalClient) CreateOpenAIEval(
	ctx context.Context,
	request *CreateOpenAIEvalRequest,
) (*OpenAIEval, error) {
	return doRequestTyped[OpenAIEval](c, ctx, http.MethodPost, pathOpenAIEvals, nil, request, "")
}

// ListOpenAIEvals lists OpenAI eval definitions.
func (c *EvalClient) ListOpenAIEvals(ctx context.Context, limit int) (*OpenAIEvalList, error) {
	all := &OpenAIEvalList{}
	err := collectPages(limit, func(query map[string]string) (int, bool, string, error) {
		page, err := doRequestTyped[OpenAIEvalList](
			c, ctx, http.MethodGet, pathOpenAIEvals, query, nil, "")
		if err != nil {
			return 0, false, "", err
		}
		all.Data = append(all.Data, page.Data...)
		return len(page.Data), page.HasMore, page.LastID, nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

// collectPages walks an OpenAI-shaped listing until the service stops offering
// a cursor, or until limit rows have been gathered.
//
// A listing that stops at the first page is a silent wrong answer rather than a
// short one: "is this name ambiguous?" and "which run is newest?" are both
// decided from these rows, so a second page nobody asked for turns a refusal
// into a wrong choice. fetch reports how many rows it added and the cursor it
// was given, so the two listings share this loop instead of a third copy.
func collectPages(
	limit int,
	fetch func(query map[string]string) (added int, hasMore bool, lastID string, err error),
) error {
	gathered := 0
	after := ""
	for {
		query := map[string]string{}
		if limit > 0 {
			query["limit"] = strconv.Itoa(limit - gathered)
		}
		if after != "" {
			query["after"] = after
		}

		added, hasMore, lastID, err := fetch(query)
		if err != nil {
			return err
		}
		gathered += added

		if !hasMore || lastID == "" || added == 0 {
			return nil
		}
		if limit > 0 && gathered >= limit {
			return nil
		}
		after = lastID
	}
}

// GetOpenAIEval gets an OpenAI eval definition.
func (c *EvalClient) GetOpenAIEval(ctx context.Context, evalID string) (*OpenAIEval, error) {
	path := pathOpenAIEvals + "/" + url.PathEscape(evalID)
	return doRequestTyped[OpenAIEval](c, ctx, http.MethodGet, path, nil, nil, "")
}

// DeleteOpenAIEval removes an eval definition and its runs.
func (c *EvalClient) DeleteOpenAIEval(ctx context.Context, evalID string) error {
	path := pathOpenAIEvals + "/" + url.PathEscape(evalID)
	_, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil, "")
	return err
}

// UpdateOpenAIEval edits an eval in place. The route is a POST on the eval
// itself, matching how this surface spells run cancel — there is no PATCH verb
// here.
//
// Only what UpdateEvalParametersBody reaches is editable: name, metadata and
// properties. Anything else the service drops silently, so substance never
// travels through this call and an edit that touches it is a new eval.
func (c *EvalClient) UpdateOpenAIEval(
	ctx context.Context,
	evalID string,
	request *UpdateOpenAIEvalRequest,
) (*OpenAIEval, error) {
	path := pathOpenAIEvals + "/" + url.PathEscape(evalID)
	return doRequestTyped[OpenAIEval](c, ctx, http.MethodPost, path, nil, request, "")
}

// CreateOpenAIEvalRun starts a run for an OpenAI eval definition.
func (c *EvalClient) CreateOpenAIEvalRun(
	ctx context.Context,
	evalID string,
	request *CreateOpenAIEvalRunRequest,
) (*OpenAIEvalRun, error) {
	path := fmt.Sprintf("%s/%s/runs", pathOpenAIEvals, url.PathEscape(evalID))
	return doRequestTyped[OpenAIEvalRun](c, ctx, http.MethodPost, path, nil, request, "")
}

// ListOpenAIEvalRuns lists runs for an OpenAI eval definition.
func (c *EvalClient) ListOpenAIEvalRuns(
	ctx context.Context,
	evalID string,
	limit int,
) (*OpenAIEvalRunList, error) {
	path := fmt.Sprintf("%s/%s/runs", pathOpenAIEvals, url.PathEscape(evalID))
	all := &OpenAIEvalRunList{}
	err := collectPages(limit, func(query map[string]string) (int, bool, string, error) {
		page, err := doRequestTyped[OpenAIEvalRunList](
			c, ctx, http.MethodGet, path, query, nil, "")
		if err != nil {
			return 0, false, "", err
		}
		all.Data = append(all.Data, page.Data...)
		return len(page.Data), page.HasMore, page.LastID, nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

// GetOpenAIEvalRun gets a run for an OpenAI eval definition.
func (c *EvalClient) GetOpenAIEvalRun(
	ctx context.Context,
	evalID string,
	runID string,
) (*OpenAIEvalRun, error) {
	path := fmt.Sprintf("%s/%s/runs/%s", pathOpenAIEvals, url.PathEscape(evalID), url.PathEscape(runID))
	return doRequestTyped[OpenAIEvalRun](c, ctx, http.MethodGet, path, nil, nil, "")
}

func (c *EvalClient) doRequest(
	ctx context.Context,
	method string,
	path string,
	query map[string]string,
	body any,
	apiVersion string,
) ([]byte, error) {
	return c.doRequestWithHeaders(ctx, method, path, query, body, apiVersion, nil)
}

// doRequestWithHeaders is doRequest with extra request headers, which the
// preview evaluator operations need to opt in to the properties they set.
func (c *EvalClient) doRequestWithHeaders(
	ctx context.Context,
	method string,
	path string,
	query map[string]string,
	body any,
	apiVersion string,
	headers map[string]string,
) ([]byte, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, messages.InvalidEndpointURL(err)
	}

	// Callers escape the ids they interpolate, so the path is set as the raw
	// one. Assigning it to u.Path re-escapes the percent signs, and an
	// evaluator named "my evaluator" then addresses one named "my%20evaluator".
	escapedPath := u.EscapedPath() + path
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return nil, messages.InvalidRequestPath(escapedPath, err)
	}
	u.Path, u.RawPath = decodedPath, escapedPath

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
		return nil, messages.CreatingRequest(err)
	}
	for k, v := range headers {
		req.Raw().Header.Set(k, v)
	}

	log.Printf("[eval_api] %s %s", method, u.Redacted())

	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, messages.MarshalingRequest(err)
		}
		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
			return nil, messages.SettingRequestBody(err)
		}
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, messages.RequestFailed(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, messages.ReadingResponseBody(err)
	}

	log.Printf("[eval_api] response status: %d", resp.StatusCode)

	// 204 belongs here: a delete that removed the resource answers No Content,
	// and treating that as a failure reports every successful delete as an
	// error. doRequestTyped already tolerates the empty body.
	if !runtime.HasStatusCode(resp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent) {
		// Restore the body so runtime.NewResponseError can read it.
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil, messages.ServiceRefused(resp.StatusCode, runtime.NewResponseError(resp))
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
		return nil, messages.ParsingResponse(err)
	}

	return &result, nil
}
