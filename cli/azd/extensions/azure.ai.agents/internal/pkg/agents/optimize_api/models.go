// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

import "encoding/json"

// Optimization job status constants.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"

	// StatusQueued is a deprecated alias for StatusPending.
	// The API returns "pending", not "queued".
	StatusQueued = StatusPending
)

// IsTerminal returns true if the status represents a terminal state.
func IsTerminal(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

// --- Request models ---

// OptimizeRequest is the top-level payload sent to POST /optimize.
type OptimizeRequest struct {
	Agent                      AgentDefinition   `json:"agent"`
	Dataset                    []DatasetTask     `json:"dataset,omitempty"`
	TrainDatasetReference      *DatasetReference `json:"trainDatasetReference,omitempty"`
	ValidationDatasetReference *DatasetReference `json:"validationDatasetReference,omitempty"`
	Evaluators                 []string          `json:"evaluators,omitempty"`
	Criteria                   []Criterion       `json:"criteria,omitempty"`
	Options                    OptimizeOptions   `json:"options"`
}

// AgentDefinition identifies the agent to optimize.
type AgentDefinition struct {
	FoundryProjectURL string            `json:"foundryProjectUrl"`
	AgentName         string            `json:"agentName"`
	AgentVersion      string            `json:"agentVersion,omitempty"`
	Model             string            `json:"model,omitempty"`
	SystemPrompt      string            `json:"systemPrompt,omitempty"`
	Skills            []SkillDefinition `json:"skills,omitempty"`
}

// SkillDefinition describes a skill attached to an agent.
type SkillDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
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
	Budget               int      `json:"budget,omitempty"`
	MaxIterations        int      `json:"maxIterations,omitempty"`
	MinImprovement       float64  `json:"minImprovement,omitempty"`
	ImprovementThreshold float64  `json:"improvementThreshold,omitempty"`
	PassThreshold        float64  `json:"passThreshold,omitempty"`
	EvalModel            string   `json:"evalModel"`
	Strategies           []string `json:"strategies,omitempty"`
	KeepVersions         bool     `json:"keepVersions,omitempty"`
	TasksPerIteration    int      `json:"tasksPerIteration,omitempty"`
	MaxReflectionTasks   int      `json:"maxReflectionTasks,omitempty"`
	ReflectionModel      string   `json:"reflectionModel,omitempty"`
	Mode                 string   `json:"mode,omitempty"`
}

// --- Response models ---

// OptimizeResponse is the immediate response from POST /optimize.
type OptimizeResponse struct {
	OperationID string `json:"operationId"`
	Status      string `json:"status"`
}

// OptimizeJobStatus is the full status of an optimization job.
type OptimizeJobStatus struct {
	OperationID         string            `json:"operationId"`
	Status              string            `json:"status"`
	CreatedAt           string            `json:"createdAt"`
	UpdatedAt           string            `json:"updatedAt"`
	Agent               *AgentDefinition  `json:"agent,omitempty"`
	Progress            *JobProgress      `json:"progress,omitempty"`
	Error               *JobError         `json:"error,omitempty"`
	Baseline            *CandidateResult  `json:"baseline,omitempty"`
	Best                *CandidateResult  `json:"best,omitempty"`
	Candidates          []CandidateResult `json:"candidates,omitempty"`
	AllStrategiesFailed bool              `json:"allStrategiesFailed,omitempty"`
	Warnings            []string          `json:"warnings,omitempty"`
}

// JobProgress reports iteration-level progress.
type JobProgress struct {
	CurrentStrategy  string  `json:"currentStrategy"`
	CurrentIteration int     `json:"currentIteration"`
	TasksCompleted   int     `json:"tasksCompleted"`
	TasksTotal       int     `json:"tasksTotal"`
	BestScore        float64 `json:"bestScore"`
	ElapsedSeconds   float64 `json:"elapsedSeconds"`
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
	AvgScore    float64        `json:"avgScore"`
	AvgTokens   float64        `json:"avgTokens"`
	PassRate    float64        `json:"passRate"`
	Mutations   map[string]any `json:"mutations,omitempty"`
	Rationale   string         `json:"rationale,omitempty"`
	CandidateID string         `json:"candidateId,omitempty"`
	TaskScores  []TaskScore    `json:"taskScores,omitempty"`
}

// TaskScore captures per-task evaluation metrics.
type TaskScore struct {
	TaskName       string             `json:"taskName"`
	Scores         map[string]float64 `json:"scores"`
	CompositeScore float64            `json:"compositeScore"`
	Tokens         int                `json:"tokens"`
	Duration       float64            `json:"durationSeconds"`
	Passed         bool               `json:"passed"`
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

// DeploymentReport is sent to FAOS after a candidate is deployed,
// creating the candidate→deployment mapping.
type DeploymentReport struct {
	CandidateID     string `json:"candidateId"`
	ProjectEndpoint string `json:"projectEndpoint,omitempty"`
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
