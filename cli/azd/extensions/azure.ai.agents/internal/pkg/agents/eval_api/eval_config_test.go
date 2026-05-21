// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Validate
// ---------------------------------------------------------------------------

func TestValidate_RequiresAgentName(t *testing.T) {
	t.Parallel()

	cfg := &EvalConfig{
		Config: opteval.Config{
			Agent:            opteval.AgentRef{},
			DatasetReference: &opteval.DatasetRef{Name: "ds", Version: "v1"},
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.name is required")
}

func TestValidate_RequiresDataset(t *testing.T) {
	t.Parallel()

	cfg := &EvalConfig{
		Config: opteval.Config{
			Agent: opteval.AgentRef{Name: "agent-1"},
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dataset_file or dataset_reference is required")
}

func TestValidate_MutuallyExclusiveDataset(t *testing.T) {
	t.Parallel()

	cfg := &EvalConfig{
		Config: opteval.Config{
			Agent:            opteval.AgentRef{Name: "agent-1"},
			DatasetFile:      "tasks.jsonl",
			DatasetReference: &opteval.DatasetRef{Name: "ds", Version: "v1"},
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestValidate_ValidWithDatasetFile(t *testing.T) {
	t.Parallel()

	cfg := &EvalConfig{
		Config: opteval.Config{
			Agent:       opteval.AgentRef{Name: "agent-1"},
			DatasetFile: "tasks.jsonl",
		},
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidate_ValidWithDatasetReference(t *testing.T) {
	t.Parallel()

	cfg := &EvalConfig{
		Config: opteval.Config{
			Agent:            opteval.AgentRef{Name: "agent-1"},
			DatasetReference: &opteval.DatasetRef{Name: "ds", Version: "v1"},
		},
	}
	assert.NoError(t, cfg.Validate())
}

// ---------------------------------------------------------------------------
// LoadEvalConfig / WriteEvalConfig round-trip
// ---------------------------------------------------------------------------

func TestEvalConfig_RoundTrip_FullFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "eval.yaml")

	original := &EvalConfig{
		Config: opteval.Config{
			Name: "full-test",
			Agent: opteval.AgentRef{
				Name:        "booking-agent",
				Kind:        "hosted",
				Version:     "v3",
				Model:       "gpt-4.1",
				Instruction: opteval.InstructionRef{Value: "This agent handles restaurant reservations"},
			},
			DatasetReference: &opteval.DatasetRef{Name: "golden-data", Version: "v2"},
			Evaluators:       opteval.EvaluatorList{{Name: "builtin.task_adherence"}, {Name: "custom-quality"}},
		},
		Options: &opteval.Options{
			EvalModel: "gpt-4o",
		},
		MaxSamples: 75,
	}

	require.NoError(t, WriteEvalConfig(path, original))
	loaded, err := LoadEvalConfig(path)
	require.NoError(t, err)

	assert.Equal(t, "full-test", loaded.Name)
	assert.Equal(t, "booking-agent", loaded.Agent.Name)
	assert.Equal(t, agent_yaml.AgentKind("hosted"), loaded.Agent.Kind)
	assert.Equal(t, "v3", loaded.Agent.Version)
	assert.Equal(t, "gpt-4.1", loaded.Agent.Model)
	require.NotNil(t, loaded.DatasetReference)
	assert.Equal(t, "golden-data", loaded.DatasetReference.Name)
	assert.Equal(t, "v2", loaded.DatasetReference.Version)
	require.Len(t, loaded.Evaluators, 2)
	assert.Equal(t, "builtin.task_adherence", loaded.Evaluators[0].Name)
	assert.Equal(t, "custom-quality", loaded.Evaluators[1].Name)
	assert.Equal(t, "gpt-4o", loaded.Options.EvalModel)
	assert.Equal(t, "This agent handles restaurant reservations", loaded.Agent.Instruction.Value)
	assert.Equal(t, 75, loaded.MaxSamples)
}

func TestEvalConfig_RoundTrip_MinimalFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "eval.yaml")

	original := &EvalConfig{
		Config: opteval.Config{
			Agent:       opteval.AgentRef{Name: "simple-agent"},
			DatasetFile: "data.jsonl",
		},
	}

	require.NoError(t, WriteEvalConfig(path, original))
	loaded, err := LoadEvalConfig(path)
	require.NoError(t, err)

	assert.Equal(t, "simple-agent", loaded.Agent.Name)
	assert.Equal(t, "data.jsonl", loaded.DatasetFile)
	assert.Nil(t, loaded.DatasetReference)
	assert.Empty(t, loaded.Evaluators)
	assert.True(t, loaded.Agent.Instruction.IsEmpty())
	assert.Zero(t, loaded.MaxSamples)
}

func TestLoadEvalConfig_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := LoadEvalConfig("/nonexistent/path/eval.yaml")
	assert.Error(t, err)
}

func TestLoadEvalConfig_InvalidYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("{{invalid yaml}}"), 0600))
	_, err := LoadEvalConfig(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse")
}

func TestWriteEvalConfig_CreatesDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "nested", "eval.yaml")

	cfg := &EvalConfig{
		Config: opteval.Config{
			Agent: opteval.AgentRef{Name: "agent-1"},
		},
	}

	require.NoError(t, WriteEvalConfig(path, cfg))
	assert.FileExists(t, path)
}

// ---------------------------------------------------------------------------
// ToAgentTargetAdaptableEvalGroupRequest
// ---------------------------------------------------------------------------

func TestToAgentTargetAdaptableEvalGroupRequest_WithEvaluators(t *testing.T) {
	t.Parallel()

	cfg := &EvalConfig{
		Config: opteval.Config{
			Name:        "test-eval",
			Agent:       opteval.AgentRef{Name: "agent-1", Version: "v1"},
			Evaluators:  opteval.EvaluatorList{{Name: "builtin.quality"}, {Name: "custom-1"}},
			DatasetFile: "tasks.jsonl",
		},
		Options: &opteval.Options{EvalModel: "gpt-4o"},
	}

	req := cfg.ToAgentTargetAdaptableEvalGroupRequest()

	assert.Equal(t, "test-eval", req.Name)
	assert.Equal(t, "agent-1", req.Metadata["azd_agent"])
	assert.Equal(t, "v1", req.Metadata["azd_agent_version"])
	require.NotNil(t, req.DataSourceConfig)
	assert.Equal(t, "custom", req.DataSourceConfig.Type)
	require.Len(t, req.TestingCriteria, 2)
	assert.Equal(t, "azure_ai_evaluator", req.TestingCriteria[0].Type)
	assert.Equal(t, "builtin.quality", req.TestingCriteria[0].EvaluatorName)
	assert.Equal(t, "gpt-4o", req.TestingCriteria[0].InitializationParameters["model"])
	assert.Equal(t, "{{item.query}}", req.TestingCriteria[0].DataMapping["query"])
	assert.Equal(t, "custom-1", req.TestingCriteria[1].EvaluatorName)
}

func TestToAgentTargetAdaptableEvalGroupRequest_WithDatasetReference(t *testing.T) {
	t.Parallel()

	cfg := &EvalConfig{
		Config: opteval.Config{
			Name:             "ref-eval",
			Agent:            opteval.AgentRef{Name: "agent-1"},
			DatasetReference: &opteval.DatasetRef{Name: "ds", Version: "v1"},
		},
	}

	req := cfg.ToAgentTargetAdaptableEvalGroupRequest()
	// DataSourceConfig is always set with the custom schema.
	require.NotNil(t, req.DataSourceConfig)
	assert.Equal(t, "custom", req.DataSourceConfig.Type)
}

func TestToAgentTargetAdaptableEvalGroupRequest_NoEvaluators(t *testing.T) {
	t.Parallel()

	cfg := &EvalConfig{
		Config: opteval.Config{
			Name:        "test-eval",
			Agent:       opteval.AgentRef{Name: "agent-1"},
			DatasetFile: "tasks.jsonl",
		},
	}

	req := cfg.ToAgentTargetAdaptableEvalGroupRequest()
	assert.Empty(t, req.TestingCriteria)
}

func TestToAgentTargetAdaptableEvalGroupRequest_MetadataFields(t *testing.T) {
	t.Parallel()

	cfg := &EvalConfig{
		Config: opteval.Config{
			Name:        "meta-test",
			Agent:       opteval.AgentRef{Name: "my-agent", Version: "v5"},
			DatasetFile: "tasks.jsonl",
		},
	}

	req := cfg.ToAgentTargetAdaptableEvalGroupRequest()
	assert.Equal(t, "my-agent", req.Metadata["azd_agent"])
	assert.Equal(t, "v5", req.Metadata["azd_agent_version"])
}
