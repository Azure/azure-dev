// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// models.go defines the request and response types for the optimization
// service API, including job status, candidate results, agent definitions,
// dataset tasks, and skill/tool definitions.
package optimize_api

import (
	"encoding/json"
	"maps"
	"slices"
)

// APIVersion is the API version used for all optimization service calls.
const APIVersion = "v1"

// optimizeJobsPath is the base path segment for optimization job endpoints.
const optimizeJobsPath = "agent_optimization_jobs"

// Optimization job status constants.
// The server may return either the old names (pending/running/completed) or
// the new names (queued/in_progress/succeeded). Both sets are accepted.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"

	// New-style status values returned by newer server versions.
	StatusQueued     = "queued"
	StatusInProgress = "in_progress"
	StatusSucceeded  = "succeeded"
)

// IsTerminal returns true if the status represents a terminal state.
func IsTerminal(status string) bool {
	switch status {
	case StatusCompleted, StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

// --- Request models ---

// OptimizeRequest is the top-level payload sent to POST /optimize.
type OptimizeRequest struct {
	Agent             AgentIdentifier `json:"agent"`
	TrainDataset      *Dataset        `json:"train_dataset,omitempty"`
	ValidationDataset *Dataset        `json:"validation_dataset,omitempty"`
	Evaluators        []EvaluatorRef  `json:"evaluators,omitempty"`
	Options           OptimizeOptions `json:"options"`
}

// AgentIdentifier references the agent to optimize by name and optional version.
type AgentIdentifier struct {
	AgentName    string `json:"agent_name"`
	AgentVersion string `json:"agent_version,omitempty"`
}

// Dataset type discriminator values for Dataset.Type.
const (
	DatasetTypeReference = "reference"
	DatasetTypeInline    = "inline"
)

// Dataset is the optimization dataset payload. It is either a registered
// dataset reference (Type "reference", with Name/Version) or an inline set of
// items (Type "inline", with Items).
type Dataset struct {
	Type    string            `json:"type"`
	Name    string            `json:"name,omitempty"`
	Version string            `json:"version,omitempty"`
	Items   []json.RawMessage `json:"items,omitempty"`
}

// EvaluatorRef references an evaluator by name and optional version.
type EvaluatorRef struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// SkillDefinition describes a skill attached to an agent.
type SkillDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Body        string `json:"body,omitempty"`
}

// ToolDefinition is an OpenAI-format function tool definition.
// The optimizer may mutate the function's description and per-parameter
// descriptions; schema fields (name, types, required) are immutable.
type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the inner function definition of a ToolDefinition.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
}

// OptimizeOptions controls the optimization run.
type OptimizeOptions struct {
	MaxCandidates      *int                       `json:"max_candidates,omitempty"`
	EvalModel          string                     `json:"eval_model,omitempty"`
	OptimizationModel  string                     `json:"optimization_model"`
	OptimizationConfig map[string]json.RawMessage `json:"optimization_config,omitempty"`
	EvaluationLevel    string                     `json:"evaluation_level,omitempty"`
}

// --- Response models ---

// OptimizeResponse is the immediate response from POST /optimize.
type OptimizeResponse struct {
	OperationID string `json:"id"`
	Status      string `json:"status"`
}

// OptimizeJobStatus is the full status of an optimization job.
type OptimizeJobStatus struct {
	ID     string           `json:"id"`
	Status string           `json:"status"`
	Inputs *OptimizeRequest `json:"inputs,omitempty"`
	// Agent is the top-level agent identifier returned by the list endpoint,
	// where jobs are not wrapped in an "inputs" envelope.
	Agent                     *AgentIdentifier `json:"agent,omitempty"`
	Result                    *OptimizeResult  `json:"result,omitempty"`
	Progress                  *JobProgress     `json:"progress,omitempty"`
	Error                     *JobError        `json:"error,omitempty"`
	Warnings                  []string         `json:"warnings,omitempty"`
	AllTargetAttributesFailed bool             `json:"all_target_attributes_failed,omitempty"`
	CreatedAt                 int64            `json:"created_at,omitempty"`
	UpdatedAt                 int64            `json:"updated_at,omitempty"`
}

// OptimizeResult holds the optimization outcome. Baseline and Best are
// candidate IDs that reference entries in Candidates.
type OptimizeResult struct {
	Baseline   string            `json:"baseline,omitempty"`
	Best       string            `json:"best,omitempty"`
	Candidates []CandidateResult `json:"candidates,omitempty"`
}

// findCandidate returns the candidate whose CandidateID or Name matches ref,
// or nil when ref is empty or no candidate matches.
func (r *OptimizeResult) findCandidate(ref string) *CandidateResult {
	if ref == "" {
		return nil
	}
	for i := range r.Candidates {
		if r.Candidates[i].CandidateID == ref || r.Candidates[i].Name == ref {
			return &r.Candidates[i]
		}
	}
	return nil
}

// AgentName returns the agent name from the job inputs, falling back to the
// top-level agent field used by the list endpoint. Returns "" when neither is
// present.
func (s *OptimizeJobStatus) AgentName() string {
	if s.Inputs != nil && s.Inputs.Agent.AgentName != "" {
		return s.Inputs.Agent.AgentName
	}
	if s.Agent != nil {
		return s.Agent.AgentName
	}
	return ""
}

// Candidates returns the result candidates (nil-safe).
func (s *OptimizeJobStatus) Candidates() []CandidateResult {
	if s.Result == nil {
		return nil
	}
	return s.Result.Candidates
}

// BestCandidate resolves the best candidate by matching Result.Best against
// the candidate list. Returns nil when there is no result or no match.
func (s *OptimizeJobStatus) BestCandidate() *CandidateResult {
	if s.Result == nil {
		return nil
	}
	return s.Result.findCandidate(s.Result.Best)
}

// BaselineCandidate resolves the baseline candidate by matching
// Result.Baseline against the candidate list.
func (s *OptimizeJobStatus) BaselineCandidate() *CandidateResult {
	if s.Result == nil {
		return nil
	}
	return s.Result.findCandidate(s.Result.Baseline)
}

// JobProgress reports candidate-level progress for a running optimization job.
type JobProgress struct {
	CandidatesCompleted int                  `json:"candidates_completed"`
	BaselineScore       float64              `json:"baseline_score"`
	BestScore           float64              `json:"best_score"`
	ElapsedSeconds      float64              `json:"elapsed_seconds"`
	InProgressCandidate *InProgressCandidate `json:"in_progress_candidate,omitempty"`
}

// InProgressCandidate describes the candidate currently being generated or
// evaluated while an optimization job is still running.
type InProgressCandidate struct {
	// CreatedAt is the Unix timestamp (seconds) when the candidate started.
	CreatedAt int64 `json:"created_at"`
	// CandidateGenerated reports whether the candidate has been generated and
	// is now being evaluated (true) versus still being generated (false).
	CandidateGenerated bool `json:"candidate_generated"`
}

// JobError captures an error from a failed job.
// The API sometimes returns a string and sometimes an object — this handles both.
type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *JobError) UnmarshalJSON(data []byte) error {
	// Try as string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		e.Message = s
		return nil
	}
	// Try as object
	type alias JobError
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*e = JobError(a)
	return nil
}

// CandidateResult holds the evaluation result for a single candidate.
type CandidateResult struct {
	Name        string         `json:"name"`
	Mutations   map[string]any `json:"mutations,omitempty"`
	AvgScore    float64        `json:"avg_score"`
	AvgTokens   float64        `json:"avg_tokens"`
	CandidateID string         `json:"candidate_id,omitempty"`
	EvalID      string         `json:"eval_id,omitempty"`
	EvalRunID   string         `json:"eval_run_id,omitempty"`
}

// MutationKeys returns the candidate's mutation keys (the names of the agent
// attributes that were changed), sorted for stable display.
func (c *CandidateResult) MutationKeys() []string {
	if len(c.Mutations) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(c.Mutations))
}

// --- List response ---

// OptimizeListResponse is the paginated list of optimization jobs.
type OptimizeListResponse struct {
	Data    []OptimizeJobStatus `json:"data"`
	FirstID string              `json:"first_id"`
	LastID  string              `json:"last_id"`
	HasMore bool                `json:"has_more"`
}

// --- Cancel response ---

// OptimizeCancelResponse is returned when cancelling an optimization job.
type OptimizeCancelResponse struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
}

// --- Deployment report ---

// DeploymentReport is sent to the optimization service after a candidate is promoted,
// creating the candidate→deployment mapping.
type DeploymentReport struct {
	CandidateID  string `json:"-"`             // used in URL path, not serialized
	AgentName    string `json:"agent_name"`    // deployed agent name
	AgentVersion string `json:"agent_version"` // deployed agent version
}

// --- Candidate models ---

// CandidateManifest represents the candidate metadata returned by
// GET /optimize/candidates/{id}.
type CandidateManifest struct {
	Files []CandidateFile `json:"files"`
}

// CandidateFile is a single entry in the candidate manifest's files list.
type CandidateFile struct {
	Path string `json:"path"`
	Type string `json:"type"`
}
