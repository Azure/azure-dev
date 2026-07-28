// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// InsightTypeEvaluationComparison compares evaluation runs. The service also
// defines EvaluationRunClusterInsight and AgentClusterInsight, which this
// extension does not use.
const InsightTypeEvaluationComparison = "EvaluationComparison"

const pathInsights = "/insights"

// InsightRequest is the polymorphic body the service dispatches on. `type` is
// the discriminator; without it the request is rejected because the underlying
// contract is an interface.
type InsightRequest struct {
	Type            string   `json:"type"`
	EvalID          string   `json:"evalId"`
	BaselineRunID   string   `json:"baselineRunId"`
	TreatmentRunIDs []string `json:"treatmentRunIds"`
}

// CreateInsightRequest wraps the request. DisplayName is required; the service
// rejects a body without it before it looks at anything else.
type CreateInsightRequest struct {
	DisplayName string          `json:"displayName"`
	Request     *InsightRequest `json:"request"`
}

// RunSummary is one run's aggregate for a single metric.
type RunSummary struct {
	RunID             string  `json:"runId"`
	SampleCount       int     `json:"sampleCount"`
	Average           float64 `json:"average"`
	StandardDeviation float64 `json:"standardDeviation"`
}

// CompareItem is one treatment run measured against the baseline.
type CompareItem struct {
	TreatmentRunSummary *RunSummary `json:"treatmentRunSummary,omitempty"`
	DeltaEstimate       float64     `json:"deltaEstimate"`
	PValue              float64     `json:"pValue"`
	// TreatmentEffect classifies the result, e.g. TooFewSamples when the
	// sample count cannot support a conclusion.
	TreatmentEffect string `json:"treatmentEffect,omitempty"`
}

// MetricComparison is the baseline and treatments for one testing criterion.
type MetricComparison struct {
	TestingCriteria    string        `json:"testingCriteria"`
	Metric             string        `json:"metric"`
	Evaluator          string        `json:"evaluator"`
	BaselineRunSummary *RunSummary   `json:"baselineRunSummary,omitempty"`
	CompareItems       []CompareItem `json:"compareItems,omitempty"`
}

// InsightResult carries the comparison once the insight succeeds.
type InsightResult struct {
	Comparisons []MetricComparison `json:"comparisons,omitempty"`
	// Method names the statistical test, e.g. PairedTTest.
	Method string `json:"method,omitempty"`
	Type   string `json:"type,omitempty"`
	Error  any    `json:"error,omitempty"`
}

// Insight is the long-running operation the comparison runs as.
type Insight struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"displayName,omitempty"`
	State       string          `json:"state,omitempty"`
	Request     *InsightRequest `json:"request,omitempty"`
	Result      *InsightResult  `json:"result,omitempty"`
}

// Succeeded reports whether the insight finished with a result.
func (i *Insight) Succeeded() bool {
	return i != nil && i.State == "Succeeded"
}

// Terminal reports whether the insight has stopped changing.
func (i *Insight) Terminal() bool {
	if i == nil {
		return false
	}
	switch i.State {
	case "", "NotStarted", "Running", "InProgress", "Queued":
		return false
	default:
		return true
	}
}

// CreateInsight starts a comparison.
//
// The synchronous variant, POST /insights/sync, returns a 500 for this request
// shape, so the asynchronous form is the only usable one and the caller polls.
func (c *EvalClient) CreateInsight(
	ctx context.Context,
	request *CreateInsightRequest,
	apiVersion string,
) (*Insight, error) {
	return doRequestTyped[Insight](
		c, ctx, http.MethodPost, pathInsights, nil, request, apiVersion)
}

// GetInsight reads a comparison's current state.
func (c *EvalClient) GetInsight(
	ctx context.Context,
	insightID string,
	apiVersion string,
) (*Insight, error) {
	path := fmt.Sprintf("%s/%s", pathInsights, url.PathEscape(insightID))
	return doRequestTyped[Insight](c, ctx, http.MethodGet, path, nil, nil, apiVersion)
}
