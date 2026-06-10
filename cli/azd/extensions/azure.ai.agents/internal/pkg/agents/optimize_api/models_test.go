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
		Agent: AgentIdentifier{
			AgentName:    "my-agent",
			AgentVersion: "1",
		},
		TrainDataset: &Dataset{
			Type: DatasetTypeInline,
			Items: []json.RawMessage{
				json.RawMessage(`{"query":"What is 2+2?","ground_truth":"4"}`),
			},
		},
		Evaluators: []EvaluatorRef{
			{Name: "coherence"},
			{Name: "relevance", Version: "1"},
		},
		Options: OptimizeOptions{
			MaxCandidates:     new(5),
			EvalModel:         "gpt-4o-mini",
			OptimizationModel: "gpt-4o",
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err, "marshal should succeed")

	s := string(data)
	// Verify snake_case JSON tags
	for _, field := range []string{
		`"agent"`, `"agent_name"`, `"agent_version"`,
		`"train_dataset"`, `"type"`, `"items"`, `"evaluators"`,
		`"options"`, `"eval_model"`, `"max_candidates"`,
		`"optimization_model"`,
		`"ground_truth"`,
	} {
		assert.True(t, strings.Contains(s, field), "JSON should contain %s", field)
	}

	var got OptimizeRequest
	require.NoError(t, json.Unmarshal(data, &got), "unmarshal should succeed")

	assert.Equal(t, original.Agent.AgentName, got.Agent.AgentName)
	require.NotNil(t, got.TrainDataset)
	assert.Equal(t, DatasetTypeInline, got.TrainDataset.Type)
	assert.Len(t, got.TrainDataset.Items, 1)
	assert.Contains(t, string(got.TrainDataset.Items[0]), `"ground_truth"`)
	require.Len(t, got.Evaluators, 2)
	assert.Equal(t, "relevance", got.Evaluators[1].Name)
	assert.Equal(t, "1", got.Evaluators[1].Version)
	assert.Equal(t, "gpt-4o-mini", got.Options.EvalModel)
}

func TestOptimizeJobStatus_RoundTrip(t *testing.T) {
	t.Parallel()

	original := OptimizeJobStatus{
		ID:        "op-123",
		Status:    StatusSucceeded,
		CreatedAt: 1781036157,
		UpdatedAt: 1781037526,
		Inputs: &OptimizeRequest{
			Agent: AgentIdentifier{AgentName: "agent-1", AgentVersion: "1"},
			Options: OptimizeOptions{
				EvalModel:     "gpt-4o",
				MaxCandidates: new(5),
			},
			TrainDataset: &Dataset{Type: DatasetTypeReference, Name: "ds", Version: "2.0"},
			Evaluators:   []EvaluatorRef{{Name: "task_adherence"}},
		},
		Result: &OptimizeResult{
			Baseline: "cand-1",
			Best:     "cand-1",
			Candidates: []CandidateResult{
				{
					Name:        "baseline",
					AvgScore:    0.87,
					AvgTokens:   0.0,
					CandidateID: "cand-1",
					EvalID:      "eval-1",
					EvalRunID:   "evalrun-1",
				},
			},
		},
		Warnings: []string{"baseline only"},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err, "marshal should succeed")

	s := string(data)
	for _, field := range []string{
		`"id"`, `"status"`, `"created_at"`, `"updated_at"`,
		`"inputs"`, `"agent"`, `"options"`, `"train_dataset"`, `"evaluators"`,
		`"result"`, `"baseline"`, `"best"`, `"candidates"`, `"candidate_id"`,
		`"avg_score"`, `"avg_tokens"`,
		`"eval_id"`, `"eval_run_id"`, `"warnings"`,
	} {
		assert.True(t, strings.Contains(s, field), "JSON should contain %s", field)
	}

	var got OptimizeJobStatus
	require.NoError(t, json.Unmarshal(data, &got), "unmarshal should succeed")

	assert.Equal(t, "op-123", got.ID)
	assert.Equal(t, StatusSucceeded, got.Status)
	assert.Equal(t, int64(1781036157), got.CreatedAt)
	assert.Equal(t, "agent-1", got.AgentName())
	require.NotNil(t, got.Result)
	assert.Len(t, got.Candidates(), 1)
	// Baseline and Best are candidate IDs resolved against the candidate list.
	require.NotNil(t, got.BestCandidate())
	assert.InDelta(t, 0.87, got.BestCandidate().AvgScore, 0.001)
	require.NotNil(t, got.BaselineCandidate())
	assert.Equal(t, "cand-1", got.BaselineCandidate().CandidateID)
}

func TestOptimizeJobStatus_ErrorField(t *testing.T) {
	t.Parallel()

	original := OptimizeJobStatus{
		ID:     "op-err",
		Status: StatusFailed,
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
			{ID: "op-1", Status: StatusCompleted},
			{ID: "op-2", Status: StatusRunning},
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
	assert.NotContains(t, string(data), "candidate_id")
	assert.NotContains(t, string(data), "cand_abc123")

	// agent_name and agent_version must be present.
	assert.Contains(t, string(data), `"agent_name":"my-agent"`)
	assert.Contains(t, string(data), `"agent_version":"3"`)
}

func TestDeploymentReport_JSON_RoundTrip(t *testing.T) {
	t.Parallel()

	body := `{"agent_name":"test-agent","agent_version":"5"}`
	var report DeploymentReport
	require.NoError(t, json.Unmarshal([]byte(body), &report))

	assert.Equal(t, "test-agent", report.AgentName)
	assert.Equal(t, "5", report.AgentVersion)
	assert.Empty(t, report.CandidateID, "CandidateID should not be populated from JSON")
}
