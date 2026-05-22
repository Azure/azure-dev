// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimizeRequest_RoundTrip(t *testing.T) {
	t.Parallel()

	original := OptimizeRequest{
		Agent: AgentDefinition{
			FoundryProjectURL: "https://example.ai.azure.com/project/my-proj",
			AgentName:         "my-agent",
			AgentVersion:      "1",
			Model:             "gpt-4o",
			SystemPrompt:      "You are helpful",
			Skills: []SkillDefinition{
				{Name: "search", Description: "web search"},
			},
		},
		Dataset: []DatasetTask{
			{
				Name:        "task1",
				Prompt:      "What is 2+2?",
				GroundTruth: "4",
				Criteria: []Criterion{
					{Name: "accuracy", Instruction: "answer must be correct"},
				},
			},
		},
		TrainDatasetReference: &DatasetReference{
			Name:    "train-ds",
			Version: "1",
		},
		Evaluators: []string{"coherence", "relevance"},
		Criteria: []Criterion{
			{Name: "global-crit", Instruction: "be concise"},
		},
		Options: OptimizeOptions{
			MaxIterations:      new(5),
			EvalModel:          "gpt-4o-mini",
			Strategies:         []string{"prompt_mutation"},
			TargetAttributes:   []string{"prompt_mutation"},
			KeepVersions:       true,
			TasksPerIteration:  10,
			MaxReflectionTasks: 3,
			ReflectionModel:    "gpt-4o",
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err, "marshal should succeed")

	s := string(data)
	// Verify camelCase JSON tags
	for _, field := range []string{
		`"agent"`, `"foundryProjectUrl"`, `"agentName"`, `"agentVersion"`,
		`"dataset"`, `"trainDatasetReference"`, `"evaluators"`, `"criteria"`,
		`"options"`, `"evalModel"`, `"maxIterations"`,
		`"keepVersions"`,
		`"tasksPerIteration"`, `"maxReflectionTasks"`, `"reflectionModel"`,
		`"strategies"`, `"targetAttributes"`, `"groundTruth"`, `"systemPrompt"`, `"skills"`,
	} {
		assert.True(t, strings.Contains(s, field), "JSON should contain %s", field)
	}

	var got OptimizeRequest
	require.NoError(t, json.Unmarshal(data, &got), "unmarshal should succeed")

	assert.Equal(t, original.Agent.AgentName, got.Agent.AgentName)
	assert.Equal(t, original.Agent.FoundryProjectURL, got.Agent.FoundryProjectURL)
	assert.Equal(t, original.Agent.Model, got.Agent.Model)
	assert.Len(t, got.Dataset, 1)
	assert.Equal(t, "task1", got.Dataset[0].Name)
	assert.Equal(t, "4", got.Dataset[0].GroundTruth)
	assert.NotNil(t, got.TrainDatasetReference)
	assert.Equal(t, "train-ds", got.TrainDatasetReference.Name)
	assert.Equal(t, "gpt-4o-mini", got.Options.EvalModel)
	assert.True(t, got.Options.KeepVersions)
}

func TestOptimizeJobStatus_RoundTrip(t *testing.T) {
	t.Parallel()

	original := OptimizeJobStatus{
		OperationID: "op-123",
		Status:      StatusRunning,
		CreatedAt:   "2024-01-01T00:00:00Z",
		UpdatedAt:   "2024-01-01T01:00:00Z",
		Agent: &AgentDefinition{
			FoundryProjectURL: "https://example.ai.azure.com/project/p",
			AgentName:         "agent-1",
		},
		Progress: &JobProgress{
			CurrentTargetAttribute: "prompt_mutation",
			CurrentIteration:       3,
			TasksCompleted:         15,
			TasksTotal:             20,
			BestScore:              0.85,
			ElapsedSeconds:         120.5,
		},
		Baseline: &CandidateResult{
			Name:     "baseline",
			AvgScore: 0.6,
			PassRate: 0.5,
		},
		Best: &CandidateResult{
			Name:        "candidate-2",
			AvgScore:    0.9,
			AvgTokens:   150.0,
			PassRate:    0.95,
			CandidateID: "cand-2",
			Mutations:   map[string]any{"systemPrompt": "Be very helpful"},
			Rationale:   "Improved prompt clarity",
			TaskScores: []TaskScore{
				{
					TaskName:       "task1",
					Scores:         map[string]float64{"coherence": 0.9, "relevance": 0.95},
					CompositeScore: 0.925,
					Tokens:         200,
					Duration:       1.5,
					Passed:         true,
				},
			},
		},
		Candidates: []CandidateResult{
			{Name: "candidate-1", AvgScore: 0.7},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err, "marshal should succeed")

	s := string(data)
	for _, field := range []string{
		`"operationId"`, `"status"`, `"createdAt"`, `"updatedAt"`,
		`"progress"`, `"currentTargetAttribute"`, `"currentIteration"`,
		`"tasksCompleted"`, `"tasksTotal"`, `"bestScore"`, `"elapsedSeconds"`,
		`"baseline"`, `"best"`, `"candidates"`, `"candidateId"`,
		`"avgScore"`, `"avgTokens"`, `"passRate"`, `"mutations"`,
		`"rationale"`, `"taskScores"`, `"compositeScore"`, `"durationSeconds"`,
	} {
		assert.True(t, strings.Contains(s, field), "JSON should contain %s", field)
	}

	var got OptimizeJobStatus
	require.NoError(t, json.Unmarshal(data, &got), "unmarshal should succeed")

	assert.Equal(t, "op-123", got.OperationID)
	assert.Equal(t, StatusRunning, got.Status)
	assert.NotNil(t, got.Agent)
	assert.Equal(t, "agent-1", got.Agent.AgentName)
	assert.NotNil(t, got.Progress)
	assert.Equal(t, 3, got.Progress.CurrentIteration)
	assert.InDelta(t, 0.85, got.Progress.BestScore, 0.001)
	assert.NotNil(t, got.Baseline)
	assert.InDelta(t, 0.6, got.Baseline.AvgScore, 0.001)
	assert.NotNil(t, got.Best)
	assert.Equal(t, "cand-2", got.Best.CandidateID)
	assert.Len(t, got.Best.TaskScores, 1)
	assert.True(t, got.Best.TaskScores[0].Passed)
	assert.Len(t, got.Candidates, 1)
}

func TestOptimizeJobStatus_ErrorField(t *testing.T) {
	t.Parallel()

	original := OptimizeJobStatus{
		OperationID: "op-err",
		Status:      StatusFailed,
		Error: &JobError{
			Code:    "InternalError",
			Message: "something went wrong",
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var got OptimizeJobStatus
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Equal(t, StatusFailed, got.Status)
	require.NotNil(t, got.Error)
	assert.Equal(t, "InternalError", got.Error.Code)
	assert.Equal(t, "something went wrong", got.Error.Message)
}

func TestIsTerminal(t *testing.T) {
	t.Parallel()

	assert.True(t, IsTerminal(StatusCompleted))
	assert.True(t, IsTerminal(StatusFailed))
	assert.True(t, IsTerminal(StatusCancelled))
	assert.False(t, IsTerminal(StatusRunning))
	assert.False(t, IsTerminal(StatusQueued))
	assert.False(t, IsTerminal("unknown"))
}

func TestOptimizeListResponse_RoundTrip(t *testing.T) {
	t.Parallel()

	original := OptimizeListResponse{
		Data: []OptimizeJobStatus{
			{OperationID: "op-1", Status: StatusCompleted},
			{OperationID: "op-2", Status: StatusRunning},
		},
		FirstID: "op-1",
		LastID:  "op-2",
		HasMore: true,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var got OptimizeListResponse
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Len(t, got.Data, 2)
	assert.Equal(t, "op-1", got.FirstID)
	assert.Equal(t, "op-2", got.LastID)
	assert.True(t, got.HasMore)
}

// ---- DeploymentReport serialization ----

func TestDeploymentReport_JSON_ExcludesCandidateID(t *testing.T) {
	t.Parallel()

	report := DeploymentReport{
		CandidateID:  "cand_abc123",
		AgentName:    "my-agent",
		AgentVersion: "3",
	}

	data, err := json.Marshal(report)
	require.NoError(t, err)

	// CandidateID has json:"-", so it must not appear in the body.
	assert.NotContains(t, string(data), "candidateId")
	assert.NotContains(t, string(data), "cand_abc123")

	// agentName and agentVersion must be present.
	assert.Contains(t, string(data), `"agentName":"my-agent"`)
	assert.Contains(t, string(data), `"agentVersion":"3"`)
}

func TestDeploymentReport_JSON_RoundTrip(t *testing.T) {
	t.Parallel()

	body := `{"agentName":"test-agent","agentVersion":"5"}`
	var report DeploymentReport
	require.NoError(t, json.Unmarshal([]byte(body), &report))

	assert.Equal(t, "test-agent", report.AgentName)
	assert.Equal(t, "5", report.AgentVersion)
	assert.Empty(t, report.CandidateID, "CandidateID should not be populated from JSON")
}
