// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testGARequest() *OptimizeRequest {
	return &OptimizeRequest{
		TargetConfiguration:            &AgentIdentifier{Type: "foundry_agent", Name: "agent", Version: "7"},
		OptimizationModelConfiguration: ModelConfiguration{Model: "optimizer"},
		OptimizationConfiguration: OptimizationConfiguration{
			Type: "agent_optimization", Goal: "improve_quality",
			EvaluationConfiguration: &EvaluationConfiguration{
				TrainingSet: SetInput{
					Type: "target_completion",
					Source: Dataset{
						Type: DatasetTypeInline,
						TestCases: []json.RawMessage{
							json.RawMessage(`{"query":"Hi","ground_truth":"Hello"}`),
						},
					},
				},
				Evaluators:      []EvaluatorRef{{Name: "builtin.task_adherence"}},
				EvaluationModel: EvaluationModelConfiguration{Model: "judge"},
			},
			CandidateSearchConfiguration: &CandidateSearchConfiguration{MaxCandidates: new(5)},
			BaselineAgentConfiguration:   &BaselineAgentConfiguration{SystemPrompt: "Help.", CurrentModel: "baseline-model"},
			AgentOptimizationSpace:       &OptimizationSpace{TargetAttributes: []string{"instructions"}},
		},
	}
}

func TestOptimizeRequest_RoundTrip(t *testing.T) {
	t.Parallel()
	request := testGARequest()
	data, err := json.Marshal(request)
	require.NoError(t, err)
	var actual OptimizeRequest
	require.NoError(t, json.Unmarshal(data, &actual))
	assert.Equal(t, *request, actual)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &fields))
	require.Len(t, fields, 3)
	assert.Contains(t, fields, "target_configuration")
	assert.Contains(t, fields, "optimization_model_configuration")
	assert.Contains(t, fields, "optimization_configuration")
	assert.NotContains(t, fields, "inputs")
	assert.NotContains(t, fields, "agent")
	assert.NotContains(t, string(data), `"train_dataset"`)
	assert.NotContains(t, string(data), `"optimization_config"`)
	assert.Contains(t, string(data), `"test_cases"`)
}

func TestOptimizationSpaceOptionalAttributes(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{}`,
		`{"model_search_space":["alternative"]}`,
		`{"target_attributes":[]}`,
		`{"target_attributes":["instructions"]}`,
	} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			var space OptimizationSpace
			require.NoError(t, json.Unmarshal([]byte(input), &space))
			data, err := json.Marshal(space)
			require.NoError(t, err)
			assert.JSONEq(t, input, string(data))
		})
	}
}

func TestOptimizeJobStatus_GAResponse(t *testing.T) {
	t.Parallel()
	body := `{
		"id":"job-1","status":"succeeded",
		"target_configuration":{"type":"foundry_agent","name":"agent","version":"7"},
		"optimization_model_configuration":{"model":"optimizer"},
		"optimization_configuration":{"type":"agent_optimization"},
		"result":{"candidate_summary":{"baseline_id":"base","best_id":"best","completed_candidate_count":1,
			"baseline_score":0.4,"best_score":0.9},"token_usage":[],"latency_metrics":[],
			"termination_reason":"max_candidates"},
		"run_duration_ms":12500,"created_at":100,"updated_at":113
	}`
	var job OptimizeJobStatus
	require.NoError(t, json.Unmarshal([]byte(body), &job))
	assert.Equal(t, "agent", job.AgentName())
	require.NotNil(t, job.Result.CandidateSummary)
	assert.Equal(t, "best", job.Result.CandidateSummary.BestID)
	assert.Equal(t, int64(12500), job.RunDurationMS)
	assert.Empty(t, job.Candidates(), "GA jobs do not embed candidates")
	job.CandidateResults = []CandidateResult{
		{CandidateID: "base", Name: "baseline"},
		{CandidateID: "best", Name: "improved", Evaluation: &CandidateEvaluation{Score: new(0.9)}},
	}
	require.NotNil(t, job.BestCandidate())
	assert.Equal(t, new(0.9), job.BestCandidate().Score())
	assert.Equal(t, "base", job.BaselineCandidate().CandidateID)
	data, err := json.Marshal(job)
	require.NoError(t, err)
	assert.JSONEq(t, body, string(data))
}

func TestIsTerminal(t *testing.T) {
	t.Parallel()
	for _, status := range []string{StatusSucceeded, StatusFailed, StatusCancelled, StatusSkipped} {
		assert.True(t, IsTerminal(status), status)
	}
	for _, status := range []string{StatusQueued, StatusInProgress, "pending", "running", "completed", "unknown"} {
		assert.False(t, IsTerminal(status), status)
	}
}

func TestCandidateResult_GAOutput(t *testing.T) {
	t.Parallel()
	var candidate CandidateResult
	require.NoError(t, json.Unmarshal([]byte(`{
		"candidate_id":"c1","job_id":"j1","name":"candidate","status":"completed","started_at":100,
		"output":{"type":"agent_optimization","mutations":[
			{"type":"skills","value":[{"name":"help","description":"Help","body":"Be helpful"}]},
			{"type":"instructions","value":"Be precise"}]},
		"evaluation":{"score":0.92,"avg_tokens":150,"eval_id":"eval1","eval_run_id":"run1"},
		"files":[{"path":"config.json","type":"config","size_in_bytes":123}]
	}`), &candidate))
	assert.Equal(t, []string{"instructions", "skills"}, candidate.MutationKeys())
	assert.Equal(t, new(0.92), candidate.Score())
	assert.Equal(t, "eval1", candidate.Evaluation.EvalID)
	var skills []SkillDefinition
	require.NoError(t, json.Unmarshal(candidate.Output.Mutations[0].Value, &skills))
	assert.Equal(t, "help", skills[0].Name)
	assert.Equal(t, int64(123), candidate.Files[0].SizeInBytes)
	assert.Nil(t, (&CandidateResult{}).Score())
	assert.Nil(t, (&CandidateResult{}).MutationKeys())
}

func TestDeploymentReport_JSON_ExcludesCandidateID(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(DeploymentReport{CandidateID: "c1", AgentName: "agent", AgentVersion: "7"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"agent_name":"agent","agent_version":"7"}`, string(data))
}
