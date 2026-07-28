// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import "encoding/json"

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

// OperationID returns the job's operation identifier.
func (j *GenerationJob) OperationID() string {
	return j.ID
}

// NormalizedStatus returns the lowercase status, defaulting to "running".
func (j *GenerationJob) NormalizedStatus() string {
	if j.Status == "" {
		return "running"
	}
	return j.Status
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

// ParseEvaluatorResult parses a GenerationJob result into a structured EvaluatorResult.
// Returns nil if the result cannot be parsed.
func ParseEvaluatorResult(result json.RawMessage) *EvaluatorResult {
	if len(result) == 0 {
		return nil
	}
	var r EvaluatorResult
	if err := json.Unmarshal(result, &r); err != nil {
		return nil
	}
	if len(r.Definition.Dimensions) == 0 {
		return nil
	}
	return &r
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

// OpenAIEval is the response for an OpenAI eval definition.
type OpenAIEval struct {
	ID         string            `json:"id"`
	Name       string            `json:"name,omitempty"`
	CreatedAt  any               `json:"created_at,omitempty"`
	ModifiedAt any               `json:"modified_at,omitempty"`
	CreatedBy  string            `json:"created_by,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// ResolvedID returns the eval's ID, falling back to name.
func (e *OpenAIEval) ResolvedID() string {
	if e.ID != "" {
		return e.ID
	}
	return e.Name
}

// OpenAIEvalList is the response for listing OpenAI eval definitions.
type OpenAIEvalList struct {
	Data []OpenAIEval `json:"data"`
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

// EvalRunTarget describes the agent target for completions.
type EvalRunTarget struct {
	Type             string   `json:"type"`
	Name             string   `json:"name"`
	Version          *string  `json:"version"`
	ToolDescriptions []string `json:"tool_descriptions"`
}

// EvalRunDataContent holds the source reference within an EvalRunDataSource.
type EvalRunDataContent struct {
	Type    EvalRunDataContentType `json:"type"`
	ID      string                 `json:"id,omitempty"`
	Content []map[string]any       `json:"content,omitempty"`
}

// NewAgentTargetDataSource builds an EvalRunDataSource configured for agent target completions.
// The source field must be set separately via SetFileContent or SetFileID.
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

// SetFileContent sets the data source to use inline file content.
func (ds *EvalRunDataSource) SetFileContent(items []map[string]any) {
	ds.Source = &EvalRunDataContent{
		Type:    EvalRunDataContentTypeFileContent,
		Content: items,
	}
}

// SetFileID sets the data source to reference a remote dataset by ID.
func (ds *EvalRunDataSource) SetFileID(fileID string) {
	ds.Source = &EvalRunDataContent{
		Type: EvalRunDataContentTypeFileID,
		ID:   fileID,
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

	// Result summary
	ResultCounts       *EvalRunResultCounts    `json:"result_counts,omitempty"`
	PerTestingCriteria []EvalRunCriteriaResult `json:"per_testing_criteria_results,omitempty"`
	Error              any                     `json:"error,omitempty"`
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
}
