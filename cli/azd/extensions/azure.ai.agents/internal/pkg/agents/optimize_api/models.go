// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// models.go defines the request and response types for the optimization
// service API, including job status, candidate results, agent definitions,
// dataset tasks, and skill/tool definitions.
package optimize_api

import "encoding/json"

// APIVersion is the API version used for all optimization service calls.
const APIVersion = "v1"

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
	Agent                      AgentIdentifier   `json:"agent"`
	Dataset                    []json.RawMessage `json:"dataset,omitempty"`
	TrainDatasetReference      *DatasetReference `json:"trainDatasetReference,omitempty"`
	ValidationDatasetReference *DatasetReference `json:"validationDatasetReference,omitempty"`
	Evaluators                 []string          `json:"evaluators,omitempty"`
	Options                    OptimizeOptions   `json:"options"`
}

// AgentIdentifier references the agent to optimize by name and optional version.
type AgentIdentifier struct {
	AgentName    string `json:"agentName"`
	AgentVersion string `json:"agentVersion,omitempty"`
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

// DatasetTask is a single task in an inline dataset.
type DatasetTask struct {
	Name        string      `json:"name,omitempty"`
	Query       string      `json:"query,omitempty"`
	Prompt      string      `json:"prompt"`
	GroundTruth string      `json:"groundTruth,omitempty"`
	Criteria    []Criterion `json:"criteria,omitempty"`
}

// DatasetReference points to a registered dataset by name and version.
type DatasetReference struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Criterion is a named evaluation criterion.
type Criterion struct {
	Name        string `json:"name"`
	Instruction string `json:"instruction"`
}

// OptimizeOptions controls the optimization run.
type OptimizeOptions struct {
	MaxIterations      *int                       `json:"maxIterations,omitempty"`
	EvalModel          string                     `json:"evalModel,omitempty"`
	BaselineModel      string                     `json:"baselineModel,omitempty"`
	OptimizationConfig map[string]json.RawMessage `json:"optimizationConfig,omitempty"`
	OptimizationModel  string                     `json:"optimizationModel,omitempty"`
	EvaluationLevel    string                     `json:"evaluationLevel,omitempty"`
}

// --- Response models ---

// OptimizeResponse is the immediate response from POST /optimize.
type OptimizeResponse struct {
	OperationID string `json:"operationId"`
	Status      string `json:"status"`
}

// OptimizeJobStatus is the full status of an optimization job.
type OptimizeJobStatus struct {
	OperationID               string            `json:"operationId"`
	Status                    string            `json:"status"`
	CreatedAt                 string            `json:"createdAt"`
	UpdatedAt                 string            `json:"updatedAt"`
	Agent                     *AgentIdentifier  `json:"agent,omitempty"`
	Progress                  *JobProgress      `json:"progress,omitempty"`
	Error                     *JobError         `json:"error,omitempty"`
	Baseline                  *CandidateResult  `json:"baseline,omitempty"`
	Best                      *CandidateResult  `json:"best,omitempty"`
	Candidates                []CandidateResult `json:"candidates,omitempty"`
	AllTargetAttributesFailed bool              `json:"allTargetAttributesFailed,omitempty"`
	Warnings                  []string          `json:"warnings,omitempty"`
}

// JobProgress reports iteration-level progress.
type JobProgress struct {
	CurrentTargetAttribute string  `json:"currentTargetAttribute"`
	CurrentIteration       int     `json:"currentIteration"`
	TasksCompleted         int     `json:"tasksCompleted"`
	TasksTotal             int     `json:"tasksTotal"`
	BestScore              float64 `json:"bestScore"`
	ElapsedSeconds         float64 `json:"elapsedSeconds"`
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
	Name            string  `json:"name"`
	AvgScore        float64 `json:"avgScore"`
	AvgTokens       float64 `json:"avgTokens"`
	PassRate        float64 `json:"passRate"`
	IsParetoOptimal bool    `json:"isParetoOptimal,omitempty"`
	Rationale       string  `json:"rationale,omitempty"`
	CandidateID     string  `json:"candidateId,omitempty"`
}

// --- List response ---

// OptimizeListResponse is the paginated list of optimization jobs.
type OptimizeListResponse struct {
	Data    []OptimizeJobStatus `json:"data"`
	FirstID string              `json:"firstId"`
	LastID  string              `json:"lastId"`
	HasMore bool                `json:"hasMore"`
}

// --- Cancel response ---

// OptimizeCancelResponse is returned when cancelling an optimization job.
type OptimizeCancelResponse struct {
	OperationID string `json:"operationId"`
	Status      string `json:"status"`
}

// --- Deployment report ---

// DeploymentReport is sent to the optimization service after a candidate is promoted,
// creating the candidate→deployment mapping.
type DeploymentReport struct {
	CandidateID  string `json:"-"`            // used in URL path, not serialized
	AgentName    string `json:"agentName"`    // deployed agent name
	AgentVersion string `json:"agentVersion"` // deployed agent version
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
