// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// EvaluatorTypeBuiltin selects the platform-provided evaluators.
const EvaluatorTypeBuiltin = "Builtin"

// JSONSchema is the subset of JSON Schema the evaluator contract uses.
type JSONSchema struct {
	Type       string         `json:"type,omitempty"`
	Required   []string       `json:"required,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

// PropertyNames returns the accepted property names, sorted for stable output.
func (s *JSONSchema) PropertyNames() []string {
	if s == nil {
		return nil
	}
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Accepts reports whether the schema declares the named property.
func (s *JSONSchema) Accepts(name string) bool {
	if s == nil || s.Properties == nil {
		return false
	}
	_, ok := s.Properties[name]
	return ok
}

// EvaluatorContract is the published input contract for an evaluator: which
// data fields it consumes and which initialization parameters it takes.
type EvaluatorContract struct {
	Type string `json:"type,omitempty"`
	// PassThreshold is a pointer because an absent threshold and a zero one are
	// different claims: zero passes every sample, absent defers to the
	// `threshold:` init parameter on the criterion that uses this evaluator.
	PassThreshold  *float64    `json:"pass_threshold,omitempty"`
	DataSchema     *JSONSchema `json:"data_schema,omitempty"`
	InitParameters *JSONSchema `json:"init_parameters,omitempty"`
}

// EvaluatorSummary is a single entry in an evaluator listing.
//
// The listing carries the full contract, so callers can shape a request to
// match an evaluator instead of guessing and taking a service-side rejection.
type EvaluatorSummary struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`

	// The listing spells this evaluator_type; `type` is accepted too because
	// other evaluator payloads use it.
	EvaluatorType string `json:"evaluator_type,omitempty"`
	TypeAlias     string `json:"type,omitempty"`

	Categories                []string           `json:"categories,omitempty"`
	SupportedEvaluationLevels []string           `json:"supported_evaluation_levels,omitempty"`
	Definition                *EvaluatorContract `json:"definition,omitempty"`
}

// Type reports the evaluator kind across both spellings.
func (e *EvaluatorSummary) Type() string {
	if e.EvaluatorType != "" {
		return e.EvaluatorType
	}
	return e.TypeAlias
}

// SupportsLevel reports whether the evaluator runs at the given evaluation
// level. An evaluator that declares no levels is treated as unconstrained.
func (e *EvaluatorSummary) SupportsLevel(level string) bool {
	if level == "" || len(e.SupportedEvaluationLevels) == 0 {
		return true
	}
	for _, supported := range e.SupportedEvaluationLevels {
		if strings.EqualFold(supported, level) {
			return true
		}
	}
	return false
}

// DataSchema returns the evaluator's input schema, or nil when the listing
// did not describe one.
func (e *EvaluatorSummary) DataSchema() *JSONSchema {
	if e == nil || e.Definition == nil {
		return nil
	}
	return e.Definition.DataSchema
}

// InitSchema returns the evaluator's initialization-parameter schema, or nil
// when the listing did not describe one.
func (e *EvaluatorSummary) InitSchema() *JSONSchema {
	if e == nil || e.Definition == nil {
		return nil
	}
	return e.Definition.InitParameters
}

// EvaluatorListResponse is the paged response for an evaluator listing.
type EvaluatorListResponse struct {
	Value    []EvaluatorSummary `json:"value"`
	NextLink string             `json:"nextLink,omitempty"`
}

// ByName indexes the listing by evaluator name.
func (r *EvaluatorListResponse) ByName() map[string]*EvaluatorSummary {
	if r == nil {
		return nil
	}
	index := make(map[string]*EvaluatorSummary, len(r.Value))
	for i := range r.Value {
		index[r.Value[i].Name] = &r.Value[i]
	}
	return index
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
	first, err := doRequestTyped[EvaluatorListResponse](
		c, ctx, http.MethodGet, pathEvaluators, query, nil, apiVersion,
	)
	if err != nil {
		return nil, err
	}
	return walkNextLinks(ctx, c, first,
		func(l *EvaluatorListResponse) string { return l.NextLink },
		func(into, page *EvaluatorListResponse) { into.Value = append(into.Value, page.Value...) })
}

// ListEvaluatorVersions returns every version of one evaluator.
func (c *EvalClient) ListEvaluatorVersions(
	ctx context.Context,
	name string,
	apiVersion string,
) (*EvaluatorListResponse, error) {
	path := pathEvaluators + "/" + url.PathEscape(name) + "/versions"
	first, err := doRequestTyped[EvaluatorListResponse](
		c, ctx, http.MethodGet, path, nil, nil, apiVersion,
	)
	if err != nil {
		return nil, err
	}
	return walkNextLinks(ctx, c, first,
		func(l *EvaluatorListResponse) string { return l.NextLink },
		func(into, page *EvaluatorListResponse) { into.Value = append(into.Value, page.Value...) })
}

// LatestEvaluatorVersionNumber reports the newest registered version as an
// integer, or 0 when the evaluator is unknown or its versions are not numeric.
func (c *EvalClient) LatestEvaluatorVersionNumber(
	ctx context.Context,
	name string,
	apiVersion string,
) int {
	list, err := c.ListEvaluatorVersions(ctx, name, apiVersion)
	if err != nil || list == nil || len(list.Value) == 0 {
		return 0
	}
	number, err := strconv.Atoi(pickLatestVersion(list.Value))
	if err != nil {
		return 0
	}
	return number
}

// parseVersionNumber reads a version string as an integer, answering 0 for one
// that is not numeric.
func parseVersionNumber(version string) int {
	number, err := strconv.Atoi(version)
	if err != nil {
		return 0
	}
	return number
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
//
// The body must stay nil: this route cancels only when the body is empty, and
// updates the run's status and counters when it is not.
func (c *EvalClient) CancelOpenAIEvalRun(
	ctx context.Context,
	evalID string,
	runID string,
) (*OpenAIEvalRun, error) {
	path := fmt.Sprintf(
		"%s/%s/runs/%s",
		pathOpenAIEvals, url.PathEscape(evalID), url.PathEscape(runID),
	)
	return doRequestTyped[OpenAIEvalRun](c, ctx, http.MethodPost, path, nil, nil, "")
}

// DeleteOpenAIEvalRun removes a single run.
func (c *EvalClient) DeleteOpenAIEvalRun(ctx context.Context, evalID, runID string) error {
	path := fmt.Sprintf(
		"%s/%s/runs/%s",
		pathOpenAIEvals, url.PathEscape(evalID), url.PathEscape(runID),
	)
	_, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil, "")
	return err
}

// ListOutputItems returns a run's per-sample results.
//
// The run itself carries only totals and a per-criterion breakdown. The output
// items are the rows: each one holds the dataset item that was evaluated, what
// the target answered, and every evaluator's score, verdict and reason. Showing
// results without them can say how many failed but never which, or why.
func (c *EvalClient) ListOutputItems(
	ctx context.Context,
	evalID, runID string,
	limit int,
) (*OutputItemList, error) {
	path := fmt.Sprintf(
		"%s/%s/runs/%s/output_items",
		pathOpenAIEvals, url.PathEscape(evalID), url.PathEscape(runID),
	)

	// Pages are followed only when the service says there are more. A run of
	// 200 samples answered one page at a time would otherwise be reported as
	// however many rows fit in the first, and the mean scores computed from
	// them would be a sample of the run rather than the run.
	//
	// Through collectPages rather than a walk of its own: this had a second
	// copy of that loop, and the copy still handed back a partial list as
	// success long after the shared one was taught to refuse.
	all := &OutputItemList{}
	err := collectPages(limit, func(query map[string]string) (int, bool, string, error) {
		page, err := doRequestTyped[OutputItemList](c, ctx, http.MethodGet, path, query, nil, "")
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

// GetOutputItem reads a single evaluated row.
func (c *EvalClient) GetOutputItem(
	ctx context.Context,
	evalID, runID, itemID string,
) (*OutputItem, error) {
	path := fmt.Sprintf(
		"%s/%s/runs/%s/output_items/%s",
		pathOpenAIEvals, url.PathEscape(evalID), url.PathEscape(runID), url.PathEscape(itemID),
	)
	return doRequestTyped[OutputItem](c, ctx, http.MethodGet, path, nil, nil, "")
}
