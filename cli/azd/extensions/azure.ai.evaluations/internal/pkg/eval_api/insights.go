// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"azureaieval/internal/messages"
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

// LenientFloat is a float64 that also decodes the quoted forms the service
// uses for values JSON cannot express.
//
// A run with a single sample has an undefined standard deviation, and the
// service sends it as the string "NaN" because JSON has no NaN literal.
// Decoding that into a plain float64 fails the entire comparison — including
// the TooFewSamples verdict that exists to explain exactly this case — so a
// one-sample gate reported a parse error instead of its result.
type LenientFloat float64

func (f *LenientFloat) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" {
		*f = LenientFloat(math.NaN())
		return nil
	}
	// "NaN", "Infinity", "-Infinity" and ordinary numbers arrive quoted;
	// ParseFloat accepts all of them once the quotes are gone.
	if unquoted, err := strconv.Unquote(s); err == nil {
		s = strings.TrimSpace(unquoted)
		if s == "" {
			*f = LenientFloat(math.NaN())
			return nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return messages.ParsingNumber(string(data), err)
	}
	*f = LenientFloat(v)
	return nil
}

// MarshalJSON writes non-finite values as null. encoding/json refuses to
// marshal NaN or ±Inf at all, which would turn `-o json` into an error the
// moment a comparison contained one; null is valid JSON and reads as the
// "undefined" that a one-sample standard deviation actually is.
func (f LenientFloat) MarshalJSON() ([]byte, error) {
	if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
		return []byte("null"), nil
	}
	return json.Marshal(float64(f))
}

// Defined reports whether the value is a real number that can be shown.
func (f LenientFloat) Defined() bool {
	return !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0)
}

// RunSummary is one run's aggregate for a single metric.
type RunSummary struct {
	RunID             string       `json:"runId"`
	SampleCount       int          `json:"sampleCount"`
	Average           LenientFloat `json:"average"`
	StandardDeviation LenientFloat `json:"standardDeviation"`
}

// CompareItem is one treatment run measured against the baseline.
type CompareItem struct {
	TreatmentRunSummary *RunSummary  `json:"treatmentRunSummary,omitempty"`
	DeltaEstimate       LenientFloat `json:"deltaEstimate"`
	PValue              LenientFloat `json:"pValue"`
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
