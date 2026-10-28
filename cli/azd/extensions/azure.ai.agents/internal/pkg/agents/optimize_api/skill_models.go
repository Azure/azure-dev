// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

// SkillOptimizationType identifies trace-driven skill optimization jobs and outputs.
const SkillOptimizationType = "skill_optimization"

// SkillTraceConnectionConfiguration selects an authorized App Insights component.
type SkillTraceConnectionConfiguration struct {
	Type       string  `json:"type"`
	ResourceID string  `json:"resource_id"`
	AgentName  *string `json:"agent_name,omitempty"`
}

// SkillTraceWindow selects either a fixed half-open window or explicit trace IDs.
type SkillTraceWindow struct {
	Type          string   `json:"type"`
	StartTime     string   `json:"start_time,omitempty"`
	EndTime       string   `json:"end_time,omitempty"`
	MaxTraces     *int     `json:"max_traces,omitempty"`
	TraceIDs      []string `json:"trace_ids,omitzero"`
	LookbackHours *int     `json:"lookback_hours,omitempty"`
}

// FoundrySkillReference identifies an exact input skill version.
type FoundrySkillReference struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// FoundrySkillOutputReference includes a version only after confirmed publication.
type FoundrySkillOutputReference struct {
	Type    string  `json:"type"`
	Name    string  `json:"name"`
	Version *string `json:"version,omitempty"`
}

// SkillResult describes one independent create, update, or no-op outcome.
type SkillResult struct {
	ChangeType  string                      `json:"change_type"`
	Status      string                      `json:"status"`
	InputSkill  *FoundrySkillReference      `json:"input_skill,omitempty"`
	OutputSkill FoundrySkillOutputReference `json:"output_skill"`
	Validations []SkillValidation           `json:"validations,omitzero"`
	Error       *JobError                   `json:"error,omitempty"`
	Rationale   string                      `json:"rationale,omitempty"`
}

// SkillValidation reports a check's actual coverage, not an implicit success.
type SkillValidation struct {
	Type      string   `json:"type"`
	ID        string   `json:"id"`
	Status    string   `json:"status"`
	Details   string   `json:"details"`
	ErrorCode string   `json:"error_code,omitempty"`
	Score     *float64 `json:"score,omitempty"`
}

// SkillResolvedContext is the resolved evidence cohort and discovered baselines.
type SkillResolvedContext struct {
	SourceTraceIDs []string                `json:"source_trace_ids,omitzero"`
	AgentSkills    []FoundrySkillReference `json:"agent_skills,omitzero"`
}

// SkillExecutionSummary is the candidate's latest persisted execution snapshot.
type SkillExecutionSummary struct {
	UpdatedAt int64                `json:"updated_at"`
	Steps     []SkillExecutionStep `json:"steps"`
}

// SkillExecutionStep is the latest state of one logical pipeline step.
type SkillExecutionStep struct {
	Name        string                     `json:"name"`
	Status      string                     `json:"status"`
	Summary     string                     `json:"summary,omitempty"`
	StepDetails *SkillExecutionStepDetails `json:"step_details,omitempty"`
	ErrorCode   string                     `json:"error_code,omitempty"`
}

// SkillExecutionStepDetails preserves measured counts; unknown counts remain absent.
type SkillExecutionStepDetails struct {
	DiscoveredSkillsCount            *int `json:"discovered_skills_count,omitempty"`
	AdditionalSkillsCount            *int `json:"additional_skills_count,omitempty"`
	EffectiveSkillsCount             *int `json:"effective_skills_count,omitempty"`
	SelectedTracesCount              *int `json:"selected_traces_count,omitempty"`
	TracesWithHumanAnnotationsCount  *int `json:"traces_with_human_annotations_count,omitempty"`
	TracesWithEvaluationResultsCount *int `json:"traces_with_evaluation_results_count,omitempty"`
	TracesWithFeedbackCount          *int `json:"traces_with_feedback_count,omitempty"`
	PreparedTracesCount              *int `json:"prepared_traces_count,omitempty"`
	ExcludedTracesCount              *int `json:"excluded_traces_count,omitempty"`
	CoveredTracesCount               *int `json:"covered_traces_count,omitempty"`
	GoodTracesCount                  *int `json:"good_traces_count,omitempty"`
	BadTracesCount                   *int `json:"bad_traces_count,omitempty"`
	LearningsCount                   *int `json:"learnings_count,omitempty"`
	PlannedCreatesCount              *int `json:"planned_creates_count,omitempty"`
	PlannedUpdatesCount              *int `json:"planned_updates_count,omitempty"`
	ValidatedSkillsCount             *int `json:"validated_skills_count,omitempty"`
	RejectedSkillsCount              *int `json:"rejected_skills_count,omitempty"`
	NoChangeSkillsCount              *int `json:"no_change_skills_count,omitempty"`
	PublishedSkillsCount             *int `json:"published_skills_count,omitempty"`
}
