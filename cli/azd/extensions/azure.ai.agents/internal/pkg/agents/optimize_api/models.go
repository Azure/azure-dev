// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package optimize_api models the GA v1 agent optimization API.
package optimize_api

import (
	"encoding/json"
	"slices"
)

// APIVersion is the stable optimization API version.
const APIVersion = "v1"

const optimizeJobsPath = "agent_optimization_jobs"

// Optimization job lifecycle states.
const (
	StatusQueued     = "queued"
	StatusInProgress = "in_progress"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
	StatusSkipped    = "skipped"
)

// IsTerminal reports whether a GA job has finished.
func IsTerminal(status string) bool {
	return slices.Contains([]string{StatusSucceeded, StatusFailed, StatusCancelled, StatusSkipped}, status)
}

// OptimizeRequest is the unwrapped body of POST /agent_optimization_jobs.
type OptimizeRequest struct {
	DisplayName                    string                    `json:"display_name,omitempty"`
	TargetConfiguration            *AgentIdentifier          `json:"target_configuration,omitempty"`
	OptimizationModelConfiguration ModelConfiguration        `json:"optimization_model_configuration"`
	OptimizationConfiguration      OptimizationConfiguration `json:"optimization_configuration"`
}

// AgentIdentifier references the deployed Foundry agent.
type AgentIdentifier struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ModelConfiguration selects an optimization model deployment.
type ModelConfiguration struct {
	Model string `json:"model"`
}

// OptimizationConfiguration controls agent or prompt optimization.
type OptimizationConfiguration struct {
	Type                         string                        `json:"type"`
	Goal                         string                        `json:"goal,omitempty"`
	EvaluationConfiguration      *EvaluationConfiguration      `json:"evaluation_configuration,omitempty"`
	CandidateSearchConfiguration *CandidateSearchConfiguration `json:"candidate_search_configuration,omitempty"`
	BaselineAgentConfiguration   *BaselineAgentConfiguration   `json:"baseline_agent_configuration,omitempty"`
	AgentOptimizationSpace       *OptimizationSpace            `json:"agent_optimization_space,omitempty"`
	Context                      []OptimizationContext         `json:"context,omitempty"`
}

// OptimizationContext supplies steering instructions for prompt optimization.
type OptimizationContext struct {
	Type   string `json:"type"`
	Prompt string `json:"prompt,omitempty"`
}

// CandidateSearchConfiguration controls candidate search limits.
type CandidateSearchConfiguration struct {
	MaxCandidates                        *int `json:"max_candidates,omitempty"`
	CandidateScreeningDataSampleSize     *int `json:"candidate_screening_data_sample_size,omitempty"`
	MaxConsecutiveFailedCandidateScreens *int `json:"max_consecutive_failed_candidate_screens,omitempty"`
}

// BaselineAgentConfiguration supplies values for hosted agents.
type BaselineAgentConfiguration struct {
	SystemPrompt string            `json:"system_prompt,omitempty"`
	CurrentModel string            `json:"current_model,omitempty"`
	Skills       []SkillDefinition `json:"skills,omitempty"`
	Tools        []ToolDefinition  `json:"tools,omitempty"`
}

// OptimizationSpace declares mutable agent attributes and alternative models.
type OptimizationSpace struct {
	TargetAttributes []string `json:"target_attributes,omitzero"`
	ModelSearchSpace []string `json:"model_search_space,omitempty"`
}

// EvaluationConfiguration defines the evaluation used during optimization.
type EvaluationConfiguration struct {
	TrainingSet            SetInput                     `json:"training_set"`
	ValidationSet          *SetInput                    `json:"validation_set,omitempty"`
	Evaluators             []EvaluatorRef               `json:"evaluators"`
	EvaluationModel        EvaluationModelConfiguration `json:"evaluation_model"`
	MaxConcurrentAgentRuns *int                         `json:"max_concurrent_agent_runs,omitempty"`
}

// EvaluationModelConfiguration selects the judge and its sampling settings.
type EvaluationModelConfiguration struct {
	Model          string               `json:"model"`
	SamplingParams *ModelSamplingParams `json:"sampling_params,omitempty"`
}

// ModelSamplingParams contains explicit model sampling overrides.
type ModelSamplingParams struct {
	Temperature         *float64 `json:"temperature,omitempty"`
	TopP                *float64 `json:"top_p,omitempty"`
	Seed                *int     `json:"seed,omitempty"`
	MaxCompletionTokens *int     `json:"max_completion_tokens,omitempty"`
}

// SetInput is a target-completion or conversation-simulation evaluation set.
type SetInput struct {
	Type                           string                   `json:"type"`
	Source                         Dataset                  `json:"source"`
	DefaultSimulationConfiguration *SimulationConfiguration `json:"default_simulation_configuration,omitempty"`
}

// SimulationConfiguration controls text conversation simulation.
type SimulationConfiguration struct {
	MaxNumTurns             *int `json:"max_num_turns,omitempty"`
	ConversationRepetitions *int `json:"conversation_repetitions,omitempty"`
	DesiredNumTurns         *int `json:"desired_num_turns,omitempty"`
}

// Dataset source discriminators.
const (
	DatasetTypeReference = "dataset_reference"
	DatasetTypeInline    = "inline"
)

// Dataset is inline test cases or an explicitly versioned Foundry dataset.
type Dataset struct {
	Type      string            `json:"type"`
	Name      string            `json:"name,omitempty"`
	Version   string            `json:"version,omitempty"`
	TestCases []json.RawMessage `json:"test_cases,omitempty"`
}

// EvaluatorRef selects a built-in or versioned custom evaluator.
type EvaluatorRef struct {
	Name                     string         `json:"name"`
	Version                  string         `json:"version,omitempty"`
	InitializationParameters map[string]any `json:"initialization_parameters,omitempty"`
}

// SkillDefinition is a named skill included in a baseline.
type SkillDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body,omitempty"`
}

// ToolDefinition is an OpenAI-format function tool.
type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes an optimizable function.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
}

// OptimizeResponse is the complete GA job returned by create.
type OptimizeResponse = OptimizeJobStatus

// OptimizeCancelResponse is the complete GA job returned by cancel.
type OptimizeCancelResponse = OptimizeJobStatus

// OptimizeJobStatus is the GA job returned by create, get, list, and cancel.
type OptimizeJobStatus struct {
	ID                             string                    `json:"id"`
	Status                         string                    `json:"status"`
	Error                          *JobError                 `json:"error,omitempty"`
	DisplayName                    string                    `json:"display_name,omitempty"`
	TargetConfiguration            *AgentIdentifier          `json:"target_configuration,omitempty"`
	OptimizationModelConfiguration ModelConfiguration        `json:"optimization_model_configuration"`
	OptimizationConfiguration      OptimizationConfiguration `json:"optimization_configuration"`
	Result                         *OptimizeResult           `json:"result,omitempty"`
	ActivityProgress               *ActivityProgress         `json:"activity_progress,omitempty"`
	Warnings                       []string                  `json:"warnings,omitempty"`
	RunDurationMS                  int64                     `json:"run_duration_ms"`
	CreatedAt                      int64                     `json:"created_at"`
	UpdatedAt                      int64                     `json:"updated_at"`
	// CandidateResults is loaded separately for terminal rendering, never added to the GA job JSON.
	CandidateResults []CandidateResult `json:"-"`
	raw              json.RawMessage
}

// MarshalJSON preserves the GA response, including fields unused by terminal rendering.
func (s OptimizeJobStatus) MarshalJSON() ([]byte, error) {
	if len(s.raw) != 0 {
		return s.raw, nil
	}
	type job OptimizeJobStatus
	return json.Marshal(job(s))
}

// AgentName returns the target's deployed name.
func (s *OptimizeJobStatus) AgentName() string {
	if s.TargetConfiguration != nil {
		return s.TargetConfiguration.Name
	}
	return ""
}

// Candidates returns candidates loaded from the separate GA candidates resource.
func (s *OptimizeJobStatus) Candidates() []CandidateResult {
	return s.CandidateResults
}

// BestCandidate resolves the summary's best candidate ID.
func (s *OptimizeJobStatus) BestCandidate() *CandidateResult {
	if s.Result == nil || s.Result.CandidateSummary == nil {
		return nil
	}
	return s.findCandidate(s.Result.CandidateSummary.BestID)
}

// BaselineCandidate resolves the summary's baseline candidate ID.
func (s *OptimizeJobStatus) BaselineCandidate() *CandidateResult {
	if s.Result == nil || s.Result.CandidateSummary == nil {
		return nil
	}
	return s.findCandidate(s.Result.CandidateSummary.BaselineID)
}

func (s *OptimizeJobStatus) findCandidate(id string) *CandidateResult {
	if id != "" {
		for i := range s.CandidateResults {
			if s.CandidateResults[i].CandidateID == id {
				return &s.CandidateResults[i]
			}
		}
	}
	return nil
}

// OptimizeResult contains aggregate results, not embedded candidates.
type OptimizeResult struct {
	CandidateSummary  *CandidateSummary `json:"candidate_summary,omitempty"`
	TokenUsage        []JobTokenUsage   `json:"token_usage"`
	LatencyMetrics    []JobLatency      `json:"latency_metrics"`
	TerminationReason string            `json:"termination_reason,omitempty"`
}

// CandidateSummary identifies the baseline and best candidate and their scores.
type CandidateSummary struct {
	CompletedCandidateCount int                     `json:"completed_candidate_count"`
	BaselineID              string                  `json:"baseline_id,omitempty"`
	BestID                  string                  `json:"best_id,omitempty"`
	BaselineScore           *float64                `json:"baseline_score,omitempty"`
	BestScore               *float64                `json:"best_score,omitempty"`
	LatestPromotedCandidate *CandidatePromotionInfo `json:"latest_promoted_candidate,omitempty"`
}

// JobTokenUsage groups tokens by optimization stage and model.
type JobTokenUsage struct {
	Stage           string `json:"stage"`
	Model           string `json:"model,omitempty"`
	InputTokens     *int64 `json:"input_tokens,omitempty"`
	OutputTokens    *int64 `json:"output_tokens,omitempty"`
	TotalTokens     *int64 `json:"total_tokens,omitempty"`
	CachedTokens    *int64 `json:"cached_tokens,omitempty"`
	ReasoningTokens *int64 `json:"reasoning_tokens,omitempty"`
}

// JobLatency groups measured call latency by stage and model.
type JobLatency struct {
	Stage        string `json:"stage"`
	Model        string `json:"model,omitempty"`
	AvgLatencyMS int64  `json:"avg_latency_ms"`
	CallCount    int64  `json:"call_count"`
}

// ActivityProgress is the service's durable display activity journal.
type ActivityProgress struct {
	TrackingStatus     string     `json:"tracking_status"`
	RecordingStoppedAt *int64     `json:"recording_stopped_at,omitempty"`
	Notice             string     `json:"notice,omitempty"`
	Activities         []Activity `json:"activities"`
	UpdatedAt          *int64     `json:"updated_at"`
	ObservedAt         int64      `json:"observed_at"`
}

// Activity is one execution or retry in the service activity journal.
type Activity struct {
	ID             string              `json:"id"`
	AttemptNumber  *int                `json:"attempt_number,omitempty"`
	Order          int                 `json:"order"`
	Title          string              `json:"title"`
	Status         string              `json:"status"`
	StartedAt      *int64              `json:"started_at"`
	CompletedAt    *int64              `json:"completed_at"`
	DurationMS     *int64              `json:"duration_ms"`
	DetailMarkdown string              `json:"detail_markdown"`
	References     []ActivityReference `json:"references"`
	ErrorCode      string              `json:"error_code,omitempty"`
	ErrorMessage   string              `json:"error_message,omitempty"`
}

// ActivityReference identifies a project-scoped resource used by an activity.
type ActivityReference struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// JobError is a structured GA job failure.
type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CandidateResult is a GA candidate, including nested output and evaluation.
type CandidateResult struct {
	CandidateID  string                  `json:"candidate_id"`
	JobID        string                  `json:"job_id"`
	Name         string                  `json:"name"`
	Status       string                  `json:"status"`
	Error        *JobError               `json:"error,omitempty"`
	StartedAt    int64                   `json:"started_at"`
	Output       *CandidateOutput        `json:"output,omitempty"`
	Rationale    string                  `json:"rationale,omitempty"`
	Files        []CandidateFile         `json:"files,omitempty"`
	AgentVersion string                  `json:"agent_version,omitempty"`
	Evaluation   *CandidateEvaluation    `json:"evaluation,omitempty"`
	Promotion    *CandidatePromotionInfo `json:"promotion,omitempty"`
}

// CandidateOutput contains the typed agent mutations.
type CandidateOutput struct {
	Type      string              `json:"type"`
	Mutations []CandidateMutation `json:"mutations,omitempty"`
}

// CandidateMutation carries a value whose shape is selected by Type.
type CandidateMutation struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value,omitempty"`
}

// CandidateEvaluation contains optional metrics; absent scores are not zero.
type CandidateEvaluation struct {
	Score           *float64           `json:"score,omitempty"`
	EvaluatorScores map[string]float64 `json:"evaluator_scores,omitempty"`
	AvgTokens       *float64           `json:"avg_tokens,omitempty"`
	AvgLatencyMS    *int64             `json:"avg_latency_ms,omitempty"`
	EvalID          string             `json:"eval_id,omitempty"`
	EvalRunID       string             `json:"eval_run_id,omitempty"`
	CompletedAt     *int64             `json:"completed_at,omitempty"`
}

// CandidatePromotionInfo records the actual promoted agent.
type CandidatePromotionInfo struct {
	PromotedAt    int64           `json:"promoted_at"`
	PromotedAgent AgentIdentifier `json:"promoted_agent"`
}

// Score returns a candidate's score, or nil before evaluation.
func (c *CandidateResult) Score() *float64 {
	if c.Evaluation != nil {
		return c.Evaluation.Score
	}
	return nil
}

// MutationKeys returns sorted mutation types for display.
func (c *CandidateResult) MutationKeys() []string {
	if c.Output == nil {
		return nil
	}
	keys := make([]string, 0, len(c.Output.Mutations))
	for _, mutation := range c.Output.Mutations {
		keys = append(keys, mutation.Type)
	}
	slices.Sort(keys)
	return keys
}

// OptimizeListResponse is a page of GA jobs.
type OptimizeListResponse struct {
	Data    []OptimizeJobStatus `json:"data"`
	FirstID string              `json:"first_id"`
	LastID  string              `json:"last_id"`
	HasMore bool                `json:"has_more"`
	raw     json.RawMessage
}

// MarshalJSON preserves the GA list envelope as returned by the service.
func (s OptimizeListResponse) MarshalJSON() ([]byte, error) {
	if len(s.raw) != 0 {
		return s.raw, nil
	}
	type page OptimizeListResponse
	return json.Marshal(page(s))
}

// CandidateListResponse is a page of GA candidates.
type CandidateListResponse struct {
	Data    []CandidateResult `json:"data"`
	FirstID string            `json:"first_id"`
	LastID  string            `json:"last_id"`
	HasMore bool              `json:"has_more"`
}

// DeploymentReport records an agent version already deployed by azd.
type DeploymentReport struct {
	CandidateID  string `json:"-"`
	AgentName    string `json:"agent_name"`
	AgentVersion string `json:"agent_version"`
}

// CandidateManifest is the GA candidate detail, including published files.
type CandidateManifest = CandidateResult

// CandidateFile is a published, candidate-scoped download descriptor.
type CandidateFile struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	SizeInBytes int64  `json:"size_in_bytes"`
}
