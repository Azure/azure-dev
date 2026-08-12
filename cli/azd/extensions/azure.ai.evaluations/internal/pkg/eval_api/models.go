// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Data Generation Jobs
// ---------------------------------------------------------------------------

// DataGenerationJobRequest is the request body for CreateDataGenerationJob.
type DataGenerationJobRequest struct {
	Inputs DataGenerationInputs `json:"inputs"`
}

// DataGenerationInputs holds the inputs for a data generation job.
type DataGenerationInputs struct {
	Name     string                `json:"name"`
	Scenario string                `json:"scenario"`
	Options  DataGenerationOptions `json:"options"`
	Sources  []GenerationSource    `json:"sources"`
}

// DataGenerationOptions holds configuration for data generation.
type DataGenerationOptions struct {
	Type         string       `json:"type"`
	MaxSamples   int          `json:"max_samples"`
	ModelOptions ModelOptions `json:"model_options"`
}

// ModelOptions holds the model selection for generation.
type ModelOptions struct {
	Model string `json:"model"`
}

// GenerationSource describes a source used for dataset or evaluator generation.
type GenerationSource struct {
	Type         string `json:"type"`
	Prompt       string `json:"prompt,omitempty"`
	AgentName    string `json:"agent_name,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	StartTime    int64  `json:"start_time,omitempty"`
}

// Agent is the part of a catalog agent that describes what it does.
//
// An agent is returned with its versions inlined rather than as a list, and
// only `latest` is populated on a plain read.
type Agent struct {
	Name     string `json:"name"`
	Versions struct {
		Latest *AgentVersion `json:"latest"`
	} `json:"versions"`
}

// AgentVersion is one published revision of an agent.
type AgentVersion struct {
	Version    string `json:"version"`
	Definition struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
	} `json:"definition"`
}

// Instructions returns the newest version's system prompt, or "" when the agent
// has no published version.
func (a *Agent) Instructions() string {
	if a == nil || a.Versions.Latest == nil {
		return ""
	}
	return strings.TrimSpace(a.Versions.Latest.Definition.Instructions)
}

// Model returns the newest version's deployment, or "" when the agent has no
// published version. It is what generation falls back to when the caller names
// no deployment of its own: the model already judged good enough to answer as
// this agent is the sensible default for writing its test cases.
func (a *Agent) Model() string {
	if a == nil || a.Versions.Latest == nil {
		return ""
	}
	return strings.TrimSpace(a.Versions.Latest.Definition.Model)
}

// GenerationJob is the response for data and evaluator generation job operations.
type GenerationJob struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *JobError       `json:"error,omitempty"`
}

// JobError captures error details from a failed generation job.
type JobError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// ResolvedNameVersion extracts the name and version from the generation job result.
// If name is empty, both return values are empty (caller should treat as no result).
// If version is empty, it defaults to "latest".
func (j *GenerationJob) ResolvedNameVersion() (string, string) {
	name := j.resultStringField("name")
	if name == "" {
		return "", ""
	}
	version := j.resultStringField("version")
	if version == "" {
		version = "latest"
	}
	return name, version
}

// resultStringField extracts a string field from the raw Result JSON.
// It first checks for a top-level key, then falls back to outputs[0].key
// to handle the nested response format.
func (j *GenerationJob) resultStringField(key string) string {
	if len(j.Result) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(j.Result, &m); err != nil {
		return ""
	}

	// Try top-level field first.
	if raw, ok := m[key]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return s
		}
	}

	// Fall back to outputs[0].key for nested response format.
	if rawOutputs, ok := m["outputs"]; ok {
		var outputs []map[string]json.RawMessage
		if err := json.Unmarshal(rawOutputs, &outputs); err == nil && len(outputs) > 0 {
			if raw, ok := outputs[0][key]; ok {
				var s string
				if err := json.Unmarshal(raw, &s); err == nil {
					return s
				}
			}
		}
	}

	return ""
}

// ---------------------------------------------------------------------------
// Evaluator Generation Jobs
// ---------------------------------------------------------------------------

// EvaluatorGenerationJobRequest is the request body for CreateEvaluatorGenerationJob.
type EvaluatorGenerationJobRequest struct {
	Inputs EvaluatorGenerationInputs `json:"inputs"`
}

// EvaluatorGenerationInputs holds the inputs for an evaluator generation job.
type EvaluatorGenerationInputs struct {
	Name          string             `json:"name"`
	EvaluatorName string             `json:"evaluator_name"`
	Category      string             `json:"category,omitempty"`
	Model         string             `json:"model"`
	Sources       []GenerationSource `json:"sources"`
}

// ---------------------------------------------------------------------------
// Evaluator Versions
// ---------------------------------------------------------------------------

// EvaluatorVersion is the response for evaluator version operations.
type EvaluatorVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ---------------------------------------------------------------------------
// Evaluator Definition (Rubric)
// ---------------------------------------------------------------------------

// EvaluatorResult is the top-level response from evaluator generation,
// containing the evaluator's definition.
type EvaluatorResult struct {
	Name       string              `json:"name"`
	Version    string              `json:"version,omitempty"`
	Definition EvaluatorDefinition `json:"definition"`
}

// EvaluatorDefinition describes an evaluator's scoring rubric.
type EvaluatorDefinition struct {
	Type       string               `json:"type"`
	Dimensions []EvaluatorDimension `json:"dimensions"`
}

// EvaluatorDimension is a single scoring dimension within a rubric evaluator.
type EvaluatorDimension struct {
	ID               string `json:"id"`
	Description      string `json:"description,omitempty"`
	Weight           int    `json:"weight"`
	AlwaysApplicable bool   `json:"always_applicable,omitempty"`
}

// ---------------------------------------------------------------------------
// Datasets
// ---------------------------------------------------------------------------

// CreateDatasetRequest is the request body for CreateDataset.
type CreateDatasetRequest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Format  string `json:"format"`
	Content string `json:"content"`
}

// Dataset is the response for dataset operations.
type Dataset struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ---------------------------------------------------------------------------
// OpenAI Evals
// ---------------------------------------------------------------------------

// DataSourceConfig describes the data source for an OpenAI eval.
type DataSourceConfig struct {
	Type                string         `json:"type"`
	ItemSchema          map[string]any `json:"item_schema"`
	IncludeSampleSchema bool           `json:"include_sample_schema"`
}

// DataSourceSchema defines the item and sample schemas for an eval data source.
type DataSourceSchema struct {
	Item   map[string]any `json:"item,omitempty"`
	Sample map[string]any `json:"sample,omitempty"`
}

// TestingCriterion describes a single evaluator in testing_criteria.
type TestingCriterion struct {
	Type                     string            `json:"type"`
	Name                     string            `json:"name"`
	EvaluatorName            string            `json:"evaluator_name"`
	EvaluatorVersion         string            `json:"evaluator_version,omitempty"`
	InitializationParameters map[string]any    `json:"initialization_parameters,omitempty"`
	DataMapping              map[string]string `json:"data_mapping,omitempty"`
}

// CreateOpenAIEvalRequest is the request body for CreateOpenAIEval.
type CreateOpenAIEvalRequest struct {
	Name             string             `json:"name"`
	Metadata         map[string]string  `json:"metadata,omitempty"`
	DataSourceConfig *DataSourceConfig  `json:"data_source_config,omitempty"`
	TestingCriteria  []TestingCriterion `json:"testing_criteria,omitempty"`
}

// UpdateOpenAIEvalRequest is UpdateEvalParametersBody: the only fields an eval
// accepts after creation. Testing criteria and the data source are fixed at
// create time, and the service drops anything else here silently rather than
// rejecting it.
type UpdateOpenAIEvalRequest struct {
	Name     string            `json:"name,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// OpenAIEval is the response for an OpenAI eval definition.
type OpenAIEval struct {
	ID         string            `json:"id"`
	Name       string            `json:"name,omitempty"`
	CreatedAt  any               `json:"created_at,omitempty"`
	ModifiedAt any               `json:"modified_at,omitempty"`
	CreatedBy  string            `json:"created_by,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// OpenAIEvalList is the response for listing OpenAI eval definitions.
type OpenAIEvalList struct {
	Data []OpenAIEval `json:"data"`
	// HasMore and LastID are the OpenAI list envelope's cursor, read the same
	// way OutputItemList reads them: only when present, so a service that sends
	// neither still yields one page.
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// ---------------------------------------------------------------------------
// OpenAI Eval Runs
// ---------------------------------------------------------------------------

// CreateOpenAIEvalRunRequest is the request body for CreateOpenAIEvalRun.
type CreateOpenAIEvalRunRequest struct {
	Name       string             `json:"name"`
	DataSource *EvalRunDataSource `json:"data_source,omitempty"`
	Metadata   map[string]string  `json:"metadata,omitempty"`
}

// EvalRunDataSourceType defines the type for an eval run data source.
type EvalRunDataSourceType string

const (
	// EvalRunDataSourceTypeAgentTarget is the data source type for agent target completions.
	EvalRunDataSourceTypeAgentTarget EvalRunDataSourceType = "azure_ai_target_completions"

	// EvalRunDataSourceTypeTraces evaluates an agent's recorded traces instead of
	// a dataset. The service reads them from Application Insights, so the agent
	// must be emitting gen_ai.input.messages / gen_ai.output.messages.
	EvalRunDataSourceTypeTraces EvalRunDataSourceType = "azure_ai_traces"

	// EvalRunDataSourceTypeResponses evaluates responses the project already
	// stored, addressed by id.
	EvalRunDataSourceTypeResponses EvalRunDataSourceType = "azure_ai_responses"

	// EvalRunDataSourceTypeJSONL scores the rows as they are, invoking nothing.
	EvalRunDataSourceTypeJSONL EvalRunDataSourceType = "jsonl"
)

// EvalRunDataContentType defines the source type for eval run data content.
type EvalRunDataContentType string

const (
	EvalRunDataContentTypeFileContent EvalRunDataContentType = "file_content"
	EvalRunDataContentTypeFileID      EvalRunDataContentType = "file_id"
)

// EvalRunDataSource describes the data source for an eval run with agent target completions.
type EvalRunDataSource struct {
	Type          EvalRunDataSourceType `json:"type"`
	InputMessages *EvalRunInputMessages `json:"input_messages,omitempty"`
	Source        *EvalRunDataContent   `json:"source,omitempty"`
	Target        *EvalRunTarget        `json:"target,omitempty"`

	// Traces only. The window is expressed as a lookback in hours, not as a
	// start bound: the service has no start_time on this data source and
	// silently falls back to its default when one is sent.
	AgentName     string `json:"agent_name,omitempty"`
	LookbackHours int    `json:"lookback_hours,omitempty"`
	EndTime       int64  `json:"end_time,omitempty"`
	MaxTraces     int    `json:"max_traces,omitempty"`

	// Responses only.
	ItemGenerationParams *ItemGenerationParams `json:"item_generation_params,omitempty"`
}

// ItemGenerationParams says how the service should turn a source into the items
// it evaluates.
type ItemGenerationParams struct {
	Type        string              `json:"type"`
	MaxNumTurns int                 `json:"max_num_turns,omitempty"`
	DataMapping map[string]string   `json:"data_mapping,omitempty"`
	Source      *EvalRunDataContent `json:"source,omitempty"`
}

// EvalRunInputMessages describes how input messages are constructed from dataset items.
type EvalRunInputMessages struct {
	Type     string                   `json:"type"`
	Template []EvalRunMessageTemplate `json:"template"`
}

// EvalRunMessageTemplate describes a single message in the input template.
type EvalRunMessageTemplate struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Type    string `json:"type"`
}

// EvalRunTarget describes what the run invokes: an agent by name, or a model
// deployment directly. Only the fields belonging to Type are sent.
type EvalRunTarget struct {
	Type             string   `json:"type"`
	Name             string   `json:"name,omitempty"`
	Version          *string  `json:"version,omitempty"`
	ToolDescriptions []string `json:"tool_descriptions,omitempty"`
	Model            string   `json:"model,omitempty"`
}

// EvalRunDataContent holds the source reference within an EvalRunDataSource.
type EvalRunDataContent struct {
	Type    EvalRunDataContentType `json:"type"`
	ID      string                 `json:"id,omitempty"`
	Content []map[string]any       `json:"content,omitempty"`
}

// NewAgentTargetDataSource builds an EvalRunDataSource configured for agent target completions.
// The rows must be supplied separately via SetFileContent.
func NewAgentTargetDataSource(agentName string, agentVersion *string) *EvalRunDataSource {
	return &EvalRunDataSource{
		Type: EvalRunDataSourceTypeAgentTarget,
		InputMessages: &EvalRunInputMessages{
			Type: "template",
			Template: []EvalRunMessageTemplate{
				{
					Role:    "user",
					Content: "{{item.query}}",
					Type:    "message",
				},
			},
		},
		Target: &EvalRunTarget{
			Type:             "azure_ai_agent",
			Name:             agentName,
			Version:          agentVersion,
			ToolDescriptions: []string{},
		},
	}
}

// NewTracesDataSource evaluates an agent's recorded traces instead of a dataset.
//
// The window is a lookback in hours. The service has no start bound on this
// data source: a start_time is accepted and dropped, leaving the default seven
// days in place, so the conversion happens here rather than being left to look
// like it worked.
func NewTracesDataSource(agentName string, lookbackHours int, end time.Time, maxTraces int) *EvalRunDataSource {
	ds := &EvalRunDataSource{
		Type:          EvalRunDataSourceTypeTraces,
		AgentName:     agentName,
		LookbackHours: lookbackHours,
		MaxTraces:     maxTraces,
	}
	if !end.IsZero() {
		ds.EndTime = end.Unix()
	}
	return ds
}

// NewDatasetOnlyDataSource scores the dataset as it stands, invoking nothing.
//
// Used when an eval declares no target: the rows already hold both sides of the
// exchange, which is how a recorded conversation is evaluated.
func NewDatasetOnlyDataSource() *EvalRunDataSource {
	return &EvalRunDataSource{Type: EvalRunDataSourceTypeJSONL}
}

// NewModelTargetDataSource sends the dataset's questions straight to a model
// deployment, with no agent in front of it.
//
// The model answers as plain text, so an eval evaluating one has to bind its
// response to {{sample.output_text}} rather than the richer output an agent
// produces.
func NewModelTargetDataSource(model string) *EvalRunDataSource {
	return &EvalRunDataSource{
		Type: EvalRunDataSourceTypeAgentTarget,
		InputMessages: &EvalRunInputMessages{
			Type: "template",
			Template: []EvalRunMessageTemplate{
				{
					Role:    "user",
					Content: "{{item.query}}",
					Type:    "message",
				},
			},
		},
		Target: &EvalRunTarget{
			Type:  "azure_ai_model",
			Model: model,
		},
	}
}

// NewResponsesDataSource evaluates responses the project already stored.
//
// The ids travel as ordinary JSONL rows and a data_mapping points the service
// at the field holding each one, which is how it retrieves the chat history
// behind the response.
func NewResponsesDataSource(responseIDs []string, maxTurns int) *EvalRunDataSource {
	rows := make([]map[string]any, 0, len(responseIDs))
	for _, id := range responseIDs {
		rows = append(rows, map[string]any{"item": map[string]any{"response_id": id}})
	}

	return &EvalRunDataSource{
		Type: EvalRunDataSourceTypeResponses,
		ItemGenerationParams: &ItemGenerationParams{
			Type:        "response_retrieval",
			MaxNumTurns: maxTurns,
			DataMapping: map[string]string{"response_id": "{{item.response_id}}"},
			Source: &EvalRunDataContent{
				Type:    EvalRunDataContentTypeFileContent,
				Content: rows,
			},
		},
	}
}

// SetFileContent sets the data source to use inline file content.
//
// There is no by-reference counterpart. A run's `file_id` means an uploaded
// file, and a dataset name is not one — sending it is rejected with "invalid
// data source file ids" — so registered datasets are fetched and sent inline
// too. See readRegisteredDataset.
func (ds *EvalRunDataSource) SetFileContent(items []map[string]any) {
	ds.Source = &EvalRunDataContent{
		Type:    EvalRunDataContentTypeFileContent,
		Content: items,
	}
}

// OpenAIEvalRun is the response for an OpenAI eval run.
type OpenAIEvalRun struct {
	ID         string             `json:"id"`
	EvalID     string             `json:"eval_id,omitempty"`
	Name       string             `json:"name,omitempty"`
	Status     string             `json:"status,omitempty"`
	CreatedAt  any                `json:"created_at,omitempty"`
	ModifiedAt any                `json:"modified_at,omitempty"`
	CreatedBy  string             `json:"created_by,omitempty"`
	DataSource *EvalRunDataSource `json:"data_source,omitempty"`
	Metadata   map[string]string  `json:"metadata,omitempty"`
	ReportURL  string             `json:"report_url,omitempty"`
	// PortalURL is built by the extension, not returned by the service, so that
	// `-o json` carries the same link the terminal prints.
	PortalURL string `json:"portal_url,omitempty"`

	// Result summary
	ResultCounts       *EvalRunResultCounts    `json:"result_counts,omitempty"`
	PerTestingCriteria []EvalRunCriteriaResult `json:"per_testing_criteria_results,omitempty"`
	Error              *JobError               `json:"error,omitempty"`
}

// Failure returns why the run failed, or "" when it did not.
//
// The field is always present and its members are null on success, so its
// presence says nothing on its own.
func (r *OpenAIEvalRun) Failure() string {
	if r == nil || r.Error == nil {
		return ""
	}
	return strings.TrimSpace(r.Error.Message)
}

// EvalRunResultCounts holds pass/fail/error/skip counts for a run.
type EvalRunResultCounts struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Errored int `json:"errored"`
	Skipped int `json:"skipped"`
}

// EvalRunCriteriaResult holds per-testing-criteria pass/fail counts.
type EvalRunCriteriaResult struct {
	TestingCriteria string `json:"testing_criteria"`
	Passed          int    `json:"passed"`
	Failed          int    `json:"failed"`
	Errored         int    `json:"errored"`
	Skipped         int    `json:"skipped"`
}

// OpenAIEvalRunList is the response for listing OpenAI eval runs.
type OpenAIEvalRunList struct {
	Data []OpenAIEvalRun `json:"data"`
	// HasMore and LastID are the OpenAI list envelope's cursor.
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// OutputItemList is a page of a run's per-sample results.
type OutputItemList struct {
	Data []OutputItem `json:"data"`
	// HasMore and LastID are the OpenAI list envelope's cursor. They are only
	// read, never required: a service that returns neither yields one page,
	// which is what this client did before it could see them at all.
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// OutputItem is one evaluated row: the dataset item, and every evaluator's
// verdict on it.
type OutputItem struct {
	ID             string         `json:"id"`
	RunID          string         `json:"run_id"`
	Status         string         `json:"status"`
	DataSourceItem map[string]any `json:"datasource_item,omitempty"`
	Results        []OutputResult `json:"results,omitempty"`
}

// OutputResult is one evaluator's verdict on one row.
type OutputResult struct {
	Name   string       `json:"name"`
	Metric string       `json:"metric,omitempty"`
	Score  LenientFloat `json:"score"`
	Label  string       `json:"label,omitempty"`
	// Passed is a pointer because an absent verdict and a failing one are
	// different claims. As a plain bool a result the service sent without one --
	// an evaluator that errored on this row -- read as a definite "fail", which
	// names the evaluator as the thing that judged badly rather than the thing
	// that did not run.
	Passed *bool `json:"passed"`
	// Reason is the judge's explanation, which is the part a failing row is
	// actually looked at for.
	Reason string `json:"reason,omitempty"`
}

// Failed reports whether this row is one to look at: any evaluator failed it,
// did not judge it, or it produced no verdict at all.
//
// A row that errored badly enough to carry no results used to answer false, so
// --failed-only hid it -- and that filter is exactly where someone looks to
// find out what went wrong. A result carrying no verdict is the same absence
// one level down.
func (o OutputItem) Failed() bool {
	if len(o.Results) == 0 {
		return true
	}
	for _, r := range o.Results {
		if !r.DidPass() {
			return true
		}
	}
	return false
}

// DidPass reports whether this result is a recorded pass. An absent verdict is
// not one.
func (r OutputResult) DidPass() bool {
	return r.Passed != nil && *r.Passed
}

// Judged reports whether the evaluator returned a verdict at all.
func (r OutputResult) Judged() bool {
	return r.Passed != nil
}

// Input renders the row's own columns for display, leaving out the
// service-injected `sample.*` bindings and the plumbing ids, which are not what
// the dataset author wrote.
func (o OutputItem) Input() string {
	if len(o.DataSourceItem) == 0 {
		return ""
	}
	skip := map[string]bool{
		"response_id": true, "agent_id": true, "agent_name": true,
		"agent_version": true, "conversation_id": true,
		"previous_response_id": true, "trace_id": true, "span_id": true,
	}
	keys := make([]string, 0, len(o.DataSourceItem))
	for k := range o.DataSourceItem {
		if skip[k] || strings.HasPrefix(k, "sample.") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, o.DataSourceItem[k]))
	}
	return strings.Join(parts, "  ")
}
