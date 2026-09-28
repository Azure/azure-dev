// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The level the caller asked for decides which rows the service produces. A
// conversation eval grades scenario seeds; everything else grades the
// query/response pairs simple_qna returns.
func TestDataGenerationType_MapsLevelToSeedType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		level string
		want  string
	}{
		{
			name:  "conversation asks for seeds",
			level: project.EvaluationLevelConversation,
			want:  eval_api.DataGenerationTypeSimulationSeed,
		},
		{
			name:  "turn keeps query/response pairs",
			level: project.EvaluationLevelTurn,
			want:  eval_api.DataGenerationTypeSimpleQnA,
		},
		{
			name:  "unstated level keeps the turn-shaped default",
			level: "",
			want:  eval_api.DataGenerationTypeSimpleQnA,
		},
		{
			name:  "a level this build does not know is not a seed request",
			level: "trajectory",
			want:  eval_api.DataGenerationTypeSimpleQnA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := dataGenerationType(tt.level); got != tt.want {
				t.Fatalf("dataGenerationType(%q) = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}

// `--evaluation-level conversation` was recorded locally but never reached the
// wire, so the service answered every request with simple_qna rows and a
// conversation eval had nothing it could grade. ADO 5631329.
func TestGenerateDataset_ConversationLevelAsksForSeedRows(t *testing.T) {
	var submitted []byte
	ec := capturingGenerationServer(t, &submitted)

	plan := agentOnlyPlan("support-agent")
	plan.EvaluationLevel = project.EvaluationLevelConversation

	var out bytes.Buffer
	var report generationReport
	_, err := ec.generateDataset(t.Context(), plan, &out, true, &report, refuseRetry)

	require.NoError(t, err)
	require.NotEmpty(t, submitted, "the job has to reach the service")

	assert.Contains(t, string(submitted), "simulation_seed",
		"a conversation eval grades scenario seeds")
	assert.NotContains(t, string(submitted), "simple_qna",
		"query/response pairs are not what a simulated conversation is built from")
}

// The turn path is the one every existing caller is on, and it must keep asking
// for exactly what it asked for before.
func TestGenerateDataset_TurnLevelStillAsksForSimpleQnA(t *testing.T) {
	var submitted []byte
	ec := capturingGenerationServer(t, &submitted)

	plan := agentOnlyPlan("support-agent")
	plan.EvaluationLevel = project.EvaluationLevelTurn

	var out bytes.Buffer
	var report generationReport
	_, err := ec.generateDataset(t.Context(), plan, &out, true, &report, refuseRetry)

	require.NoError(t, err)
	assert.Contains(t, string(submitted), "simple_qna")
	assert.NotContains(t, string(submitted), "simulation_seed")
}

// A plan that never stated a level is the pre-existing shape, and it has to
// keep submitting a type the service can answer rather than an empty one.
func TestGenerateDataset_UnstatedLevelStillAsksForSimpleQnA(t *testing.T) {
	var submitted []byte
	ec := capturingGenerationServer(t, &submitted)

	var out bytes.Buffer
	var report generationReport
	_, err := ec.generateDataset(t.Context(), agentOnlyPlan("support-agent"), &out, true, &report, refuseRetry)

	require.NoError(t, err)
	assert.Contains(t, string(submitted), "simple_qna")
}
