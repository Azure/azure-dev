// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaiagent/internal/pkg/agents/opt_eval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- hasModelConfig ----

func TestHasModelConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		oc   opt_eval.OptimizationConfig
		want bool
	}{
		{"nil config", nil, false},
		{"empty config", opt_eval.OptimizationConfig{}, false},
		{"has model_search_space key", opt_eval.OptimizationConfig{
			"model_search_space": json.RawMessage(`["gpt-4o"]`),
		}, true},
		{"has other keys only", opt_eval.OptimizationConfig{
			"system_prompt": json.RawMessage(`"hello"`),
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, hasModelConfig(tt.oc))
		})
	}
}

// ---- model JSON serialization ----

// TestModelConfigIsPlainArray verifies that when models are stored in
// OptimizationConfig["model_search_space"], the value is a JSON array of strings
// (e.g. ["gpt-4o","gpt-5"]) — not a wrapped object like {"model":[...]}.
func TestModelConfigIsPlainArray(t *testing.T) {
	t.Parallel()

	models := []string{"gpt-4o", "gpt-5"}
	modelJSON, err := json.Marshal(models)
	require.NoError(t, err)

	oc := make(opt_eval.OptimizationConfig)
	oc["model_search_space"] = modelJSON

	// Deserialize and verify it's a plain array.
	var parsed []string
	require.NoError(t, json.Unmarshal(oc["model_search_space"], &parsed))
	assert.Equal(t, models, parsed)

	// Verify it does NOT deserialize as an object with a "model" key.
	var asObject map[string]any
	err = json.Unmarshal(oc["model_search_space"], &asObject)
	assert.Error(t, err, "model_search_space value should not be a JSON object")
}

// ---- isRecommendedOptimizationModel ----

func TestIsRecommendedOptimizationModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model string
		want  bool
	}{
		{"gpt-5", true},
		{"GPT-5", true},
		{"gpt-5.1", true},
		{"gpt-5.2", true},
		{"gpt-5.4", true},
		{"gpt-5.5", true},
		{"deepseek-v4-pro", true},
		{"Deepseek-V4-Pro", true},
		{"deepseek-v3.2", true},
		{"gpt-4o", false},
		{"gpt-4o-mini", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isRecommendedOptimizationModel(tt.model))
		})
	}
}

// ---- buildOptimizeModelChoices — baseline exclusion ----
// buildOptimizeModelChoices requires a real azdClient to list deployments,
// so its baseline-exclusion logic is validated by TestResolveOptimizeTargetModels_*.

// ---- resolveOptimizeSystemPrompt — nil azdClient ----

func TestResolveOptimizeSystemPrompt_NilClient_WithInstruction(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{
				Instruction: opt_eval.InstructionRef{Value: "Be helpful"},
			},
		},
	}

	err := resolveOptimizeSystemPrompt(t.Context(), nil, cfg, "", false, false)
	assert.NoError(t, err)
}

func TestResolveOptimizeSystemPrompt_NilClient_NoInstruction(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{},
		},
	}

	err := resolveOptimizeSystemPrompt(t.Context(), nil, cfg, "", false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "instruction is required")
}

// ---- resolveOptimizeEvalModel — nil azdClient ----

func TestResolveOptimizeEvalModel_NilClient(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{Options: &opt_eval.Options{}}
	err := resolveOptimizeEvalModel(t.Context(), nil, cfg, false, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot prompt")
}

// ---- resolveOptimizeDataset — nil azdClient ----

func TestResolveOptimizeDataset_NilClient(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{Options: &opt_eval.Options{}}
	err := resolveOptimizeDataset(t.Context(), nil, cfg, "", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot prompt")
}

// ---- resolveOptimizeOptimizationModel — nil azdClient ----

func TestResolveOptimizeOptimizationModel_NilClient(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{Options: &opt_eval.Options{}}
	err := resolveOptimizeOptimizationModel(t.Context(), nil, cfg, "")
	assert.NoError(t, err) // silently skips
	assert.Empty(t, cfg.Options.OptimizationModel)
}

// ---- resolveOptimizeTargetModels — nil azdClient ----

func TestResolveOptimizeTargetModels_NilClient(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{Options: &opt_eval.Options{}}
	err := resolveOptimizeTargetModels(t.Context(), nil, cfg, "")
	assert.NoError(t, err) // silently skips
	assert.Nil(t, cfg.Options.OptimizationConfig)
}

// ---- promptOptimizeConfigConfirmation — nil azdClient ----

func TestPromptOptimizeConfigConfirmation_NilClient(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{}
	err := promptOptimizeConfigConfirmation(t.Context(), nil, cfg, "")
	assert.NoError(t, err) // silently skips
}
