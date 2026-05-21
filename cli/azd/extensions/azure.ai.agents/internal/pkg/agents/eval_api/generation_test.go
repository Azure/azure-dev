// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"testing"

	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// BuildGenerationSources
// ---------------------------------------------------------------------------

func TestBuildGenerationSources_HostedWithInstruction(t *testing.T) {
	t.Parallel()
	sources := BuildGenerationSources("hosted", "my-agent", "v2", "Test interactions", nil)
	require.Len(t, sources, 2)
	assert.Equal(t, "prompt", sources[0].Type)
	assert.Equal(t, "Test interactions", sources[0].Prompt)
	assert.Equal(t, "agent", sources[1].Type)
	assert.Equal(t, "my-agent", sources[1].AgentName)
	assert.Equal(t, "v2", sources[1].AgentVersion)
}

func TestBuildGenerationSources_NoVersion(t *testing.T) {
	t.Parallel()
	sources := BuildGenerationSources("hosted", "agent", "", "", nil)
	require.Len(t, sources, 1)
	assert.Empty(t, sources[0].AgentVersion)
}

func TestBuildGenerationSources_HostedNoInstruction(t *testing.T) {
	t.Parallel()
	sources := BuildGenerationSources("hosted", "agent", "v1", "", nil)
	require.Len(t, sources, 1)
	assert.Equal(t, "agent", sources[0].Type)
}

// ---------------------------------------------------------------------------
// NewDataGenerationJobRequest
// ---------------------------------------------------------------------------

func TestNewDataGenerationJobRequest(t *testing.T) {
	t.Parallel()
	sources := []GenerationSource{{Type: "agent", AgentName: "a1"}}
	req := NewDataGenerationJobRequest("eval-suite", "gpt-4o", 50, sources)
	assert.Equal(t, "eval-suite", req.Inputs.Name)
	assert.Equal(t, "evaluation", req.Inputs.Scenario)
	assert.Equal(t, "simple_qna", req.Inputs.Options.Type)
	assert.Equal(t, 50, req.Inputs.Options.MaxSamples)
	assert.Equal(t, "gpt-4o", req.Inputs.Options.ModelOptions.Model)
	require.Len(t, req.Inputs.Sources, 1)
}

// ---------------------------------------------------------------------------
// NewEvaluatorGenerationJobRequest
// ---------------------------------------------------------------------------

func TestNewEvaluatorGenerationJobRequest(t *testing.T) {
	t.Parallel()
	sources := []GenerationSource{{Type: "agent", AgentName: "a1"}}
	req := NewEvaluatorGenerationJobRequest("eval-suite", "gpt-4o", sources)
	assert.Equal(t, "eval-suite", req.Name)
	assert.Equal(t, "eval-suite", req.EvaluatorName)
	assert.Equal(t, "quality", req.Category)
	assert.Equal(t, "gpt-4o", req.Model)
	require.Len(t, req.Sources, 1)
}

// ---------------------------------------------------------------------------
// IsBuiltinEvaluator
// ---------------------------------------------------------------------------

func TestIsBuiltinEvaluator(t *testing.T) {
	t.Parallel()
	assert.True(t, IsBuiltinEvaluator("builtin.task_adherence"))
	assert.True(t, IsBuiltinEvaluator("builtin."))
	assert.False(t, IsBuiltinEvaluator("my-quality"))
	assert.False(t, IsBuiltinEvaluator(""))
	assert.False(t, IsBuiltinEvaluator("builtins.quality"))
}

// ---------------------------------------------------------------------------
// SplitEvaluators
// ---------------------------------------------------------------------------

func TestSplitEvaluators(t *testing.T) {
	t.Parallel()

	t.Run("mixed", func(t *testing.T) {
		t.Parallel()
		gen, bi := SplitEvaluators(opteval.EvaluatorList{
			{Name: "builtin.task_adherence"}, {Name: "my-quality"}, {Name: "builtin.safety"},
		})
		assert.Equal(t, opteval.EvaluatorList{{Name: "my-quality"}}, gen)
		assert.Equal(t, opteval.EvaluatorList{{Name: "builtin.task_adherence"}, {Name: "builtin.safety"}}, bi)
	})

	t.Run("all builtin", func(t *testing.T) {
		t.Parallel()
		gen, bi := SplitEvaluators(opteval.EvaluatorList{
			{Name: "builtin.quality"}, {Name: "builtin.safety"},
		})
		assert.Nil(t, gen)
		assert.Equal(t, opteval.EvaluatorList{{Name: "builtin.quality"}, {Name: "builtin.safety"}}, bi)
	})

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		gen, bi := SplitEvaluators(nil)
		assert.Nil(t, gen)
		assert.Nil(t, bi)
	})
}

// ---------------------------------------------------------------------------
// IsDatasetName
// ---------------------------------------------------------------------------

func TestIsDatasetName(t *testing.T) {
	t.Parallel()
	assert.True(t, IsDatasetName("eval-data-2026"))
	assert.True(t, IsDatasetName("my-dataset.v2"))
	assert.False(t, IsDatasetName("golden.jsonl"))
	assert.False(t, IsDatasetName("data.json"))
	assert.False(t, IsDatasetName("results.csv"))
	assert.False(t, IsDatasetName("./tests/golden.jsonl"))
	assert.False(t, IsDatasetName(""))
}
