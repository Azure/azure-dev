// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// EvaluatorTypeBuiltin selects the platform-provided evaluators.
const EvaluatorTypeBuiltin = "Builtin"

// EvaluatorSummary is a single entry in an evaluator listing.
type EvaluatorSummary struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// EvaluatorListResponse is the paged response for an evaluator listing.
type EvaluatorListResponse struct {
	Value    []EvaluatorSummary `json:"value"`
	NextLink string             `json:"nextLink,omitempty"`
}

// ListEvaluators returns the evaluators visible to the project. Pass
// EvaluatorTypeBuiltin to list only the platform's built-ins.
func (c *EvalClient) ListEvaluators(
	ctx context.Context,
	evaluatorType string,
	apiVersion string,
) (*EvaluatorListResponse, error) {
	var query map[string]string
	if evaluatorType != "" {
		query = map[string]string{"type": evaluatorType}
	}
	return doRequestTyped[EvaluatorListResponse](
		c, ctx, http.MethodGet, pathEvaluators, query, nil, apiVersion,
	)
}

// ListEvaluatorVersions returns every version of one evaluator.
func (c *EvalClient) ListEvaluatorVersions(
	ctx context.Context,
	name string,
	apiVersion string,
) (*EvaluatorListResponse, error) {
	path := pathEvaluators + "/" + url.PathEscape(name) + "/versions"
	return doRequestTyped[EvaluatorListResponse](
		c, ctx, http.MethodGet, path, nil, nil, apiVersion,
	)
}

// DeleteEvaluatorVersion removes a single evaluator version.
func (c *EvalClient) DeleteEvaluatorVersion(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) error {
	path := fmt.Sprintf(
		"%s/%s/versions/%s",
		pathEvaluators, url.PathEscape(name), url.PathEscape(version),
	)
	_, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil, apiVersion)
	return err
}

// CancelOpenAIEvalRun stops an in-flight run.
func (c *EvalClient) CancelOpenAIEvalRun(
	ctx context.Context,
	evalID string,
	runID string,
) (*OpenAIEvalRun, error) {
	path := fmt.Sprintf(
		"%s/%s/runs/%s/cancel",
		pathOpenAIEvals, url.PathEscape(evalID), url.PathEscape(runID),
	)
	return doRequestTyped[OpenAIEvalRun](c, ctx, http.MethodPost, path, nil, nil, "")
}
