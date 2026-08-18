// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package opt_eval

import (
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// ---------------------------------------------------------------------------
// Config Read / Write round-trip
// ---------------------------------------------------------------------------

func TestConfig_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := &Config{
		Name: "test-config",
		Agent: AgentRef{
			Name:    "my-agent",
			Kind:    agent_yaml.AgentKindHosted,
			Version: "v1",
			Model:   "gpt-4o",
		},
		DatasetFile: "tasks.jsonl",
		Evaluators:  EvaluatorList{{Name: "builtin.quality"}, {Name: "custom-1"}},
	}

	require.NoError(t, Write(path, original))
	loaded, err := Read(path)
	require.NoError(t, err)

	assert.Equal(t, "test-config", loaded.Name)
	assert.Equal(t, "my-agent", loaded.Agent.Name)
	assert.Equal(t, agent_yaml.AgentKindHosted, loaded.Agent.Kind)
	assert.Equal(t, "v1", loaded.Agent.Version)
	assert.Equal(t, "gpt-4o", loaded.Agent.Model)
	assert.Equal(t, "tasks.jsonl", loaded.DatasetFile)
	require.Len(t, loaded.Evaluators, 2)
	assert.Equal(t, "builtin.quality", loaded.Evaluators[0].Name)
	assert.Equal(t, "custom-1", loaded.Evaluators[1].Name)
}

func TestConfig_RoundTrip_MixedEvaluators(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := &Config{
		Agent: AgentRef{Name: "agent-x"},
		Evaluators: EvaluatorList{
			{Name: "builtin.task_adherence"},
			{Name: "custom-quality", Version: "2", LocalURI: "evaluators/custom-quality_2.json"},
		},
	}

	require.NoError(t, Write(path, original))
	loaded, err := Read(path)
	require.NoError(t, err)

	require.Len(t, loaded.Evaluators, 2)
	assert.Equal(t, "builtin.task_adherence", loaded.Evaluators[0].Name)
	assert.Empty(t, loaded.Evaluators[0].Version)
	assert.Empty(t, loaded.Evaluators[0].LocalURI)
	assert.Equal(t, "custom-quality", loaded.Evaluators[1].Name)
	assert.Equal(t, "2", loaded.Evaluators[1].Version)
	assert.Equal(t, "evaluators/custom-quality_2.json", loaded.Evaluators[1].LocalURI)
}

func TestEvaluatorList_Names(t *testing.T) {
	t.Parallel()
	list := EvaluatorList{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	assert.Equal(t, []string{"a", "b", "c"}, list.Names())
}

func TestEvaluatorList_FindByLocalURI(t *testing.T) {
	t.Parallel()
	list := EvaluatorList{
		{Name: "builtin.x"},
		{Name: "custom", LocalURI: "/path/to/file.json"},
		{Name: "other"},
	}
	found := list.FindByLocalURI()
	require.Len(t, found, 1)
	assert.Equal(t, "custom", found[0].Name)
}

func TestEvaluatorList_SetVersion(t *testing.T) {
	t.Parallel()
	list := EvaluatorList{{Name: "a", Version: "1"}, {Name: "b"}}
	list.SetVersion("b", "3")
	assert.Equal(t, "3", list[1].Version)
	// Non-matching name is a no-op.
	list.SetVersion("nonexistent", "99")
	assert.Equal(t, "1", list[0].Version)
}

func TestConfig_RoundTrip_DatasetReference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := &Config{
		Agent:   AgentRef{Name: "a1"},
		Dataset: &DatasetRef{Name: "golden", Version: "v2"},
	}

	require.NoError(t, Write(path, original))
	loaded, err := Read(path)
	require.NoError(t, err)

	require.NotNil(t, loaded.Dataset)
	assert.Equal(t, "golden", loaded.Dataset.Name)
	assert.Equal(t, "v2", loaded.Dataset.Version)
	assert.Empty(t, loaded.DatasetFile)
}

func TestConfig_RoundTrip_EvaluatorInitializationParameters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := &Config{
		Agent: AgentRef{Name: "agent-x"},
		Evaluators: EvaluatorList{
			// Plain evaluator — no init params, should survive as scalar.
			{Name: "builtin.task_adherence"},
			// Evaluator with initialization_parameters only (no version/local_uri).
			// MarshalYAML must emit this as a mapping, not a plain scalar,
			// otherwise the params are silently dropped on round-trip.
			{
				Name: "builtin.regex_match",
				InitializationParameters: map[string]any{
					"patterns": []any{"(?i)Answer:\\s*{{ground_truth}}"},
				},
			},
			// Evaluator with both version and init params.
			{
				Name:    "custom-quality",
				Version: "3",
				InitializationParameters: map[string]any{
					"threshold": 0.5,
				},
			},
		},
	}

	require.NoError(t, Write(path, original))
	loaded, err := Read(path)
	require.NoError(t, err)

	require.Len(t, loaded.Evaluators, 3)

	// Plain evaluator — no init params.
	assert.Equal(t, "builtin.task_adherence", loaded.Evaluators[0].Name)
	assert.Empty(t, loaded.Evaluators[0].InitializationParameters)

	// Init-params-only evaluator — must NOT be lost.
	assert.Equal(t, "builtin.regex_match", loaded.Evaluators[1].Name)
	require.NotEmpty(t, loaded.Evaluators[1].InitializationParameters, "initialization_parameters must survive YAML round-trip")

	// Version + init params.
	assert.Equal(t, "custom-quality", loaded.Evaluators[2].Name)
	assert.Equal(t, "3", loaded.Evaluators[2].Version)
	require.NotEmpty(t, loaded.Evaluators[2].InitializationParameters)
}

// ---------------------------------------------------------------------------
// DatasetRef.IsLocal / IsRemote — inferred from populated fields
// ---------------------------------------------------------------------------

func TestDatasetRef_IsLocal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ref  *DatasetRef
		want bool
	}{
		{"nil", nil, false},
		{"local_uri only", &DatasetRef{LocalURI: "./data.jsonl"}, true},
		{"name present", &DatasetRef{Name: "ds"}, false},
		{"name and local_uri", &DatasetRef{Name: "ds", LocalURI: "./cache.jsonl"}, false},
		{"empty", &DatasetRef{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.ref.IsLocal())
		})
	}
}

func TestDatasetRef_IsRemote(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ref  *DatasetRef
		want bool
	}{
		{"nil", nil, false},
		{"name present", &DatasetRef{Name: "ds"}, true},
		{"name and version", &DatasetRef{Name: "ds", Version: "2"}, true},
		{"local_uri only", &DatasetRef{LocalURI: "./data.jsonl"}, false},
		{"empty", &DatasetRef{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.ref.IsRemote())
		})
	}
}

// ---------------------------------------------------------------------------
// Config dataset helpers — NormalizeDataset / LocalDatasetPath / RemoteDatasetReference
// ---------------------------------------------------------------------------

func TestConfig_NormalizeDataset(t *testing.T) {
	t.Parallel()

	t.Run("merges legacy dataset_reference into Dataset", func(t *testing.T) {
		t.Parallel()
		c := &Config{LegacyDatasetReference: &DatasetRef{Name: "golden", Version: "2"}}
		c.NormalizeDataset()
		require.NotNil(t, c.Dataset)
		assert.Equal(t, "golden", c.Dataset.Name)
		assert.Nil(t, c.LegacyDatasetReference, "legacy field should be cleared")
	})

	t.Run("Dataset takes precedence over legacy", func(t *testing.T) {
		t.Parallel()
		c := &Config{
			Dataset:                &DatasetRef{Name: "new"},
			LegacyDatasetReference: &DatasetRef{Name: "old"},
		}
		c.NormalizeDataset()
		assert.Equal(t, "new", c.Dataset.Name)
		assert.Nil(t, c.LegacyDatasetReference)
	})

	t.Run("no-op when neither set", func(t *testing.T) {
		t.Parallel()
		c := &Config{}
		c.NormalizeDataset()
		assert.Nil(t, c.Dataset)
	})
}

func TestConfig_LocalDatasetPath(t *testing.T) {
	t.Parallel()

	t.Run("dataset_file takes precedence", func(t *testing.T) {
		t.Parallel()
		c := &Config{
			DatasetFile: "tasks.jsonl",
			Dataset:     &DatasetRef{LocalURI: "./other.jsonl"},
		}
		assert.Equal(t, "tasks.jsonl", c.LocalDatasetPath())
	})

	t.Run("falls back to local dataset", func(t *testing.T) {
		t.Parallel()
		c := &Config{Dataset: &DatasetRef{LocalURI: "./golden.jsonl"}}
		assert.Equal(t, "./golden.jsonl", c.LocalDatasetPath())
	})

	t.Run("empty for remote dataset", func(t *testing.T) {
		t.Parallel()
		c := &Config{Dataset: &DatasetRef{Name: "registered"}}
		assert.Empty(t, c.LocalDatasetPath())
	})

	t.Run("empty when nothing set", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, (&Config{}).LocalDatasetPath())
	})
}

func TestConfig_RemoteDatasetReference(t *testing.T) {
	t.Parallel()

	t.Run("returns ref for remote dataset", func(t *testing.T) {
		t.Parallel()
		c := &Config{Dataset: &DatasetRef{Name: "ds", Version: "3"}}
		ref := c.RemoteDatasetReference()
		require.NotNil(t, ref)
		assert.Equal(t, "ds", ref.Name)
	})

	t.Run("nil for local dataset", func(t *testing.T) {
		t.Parallel()
		c := &Config{Dataset: &DatasetRef{LocalURI: "./data.jsonl"}}
		assert.Nil(t, c.RemoteDatasetReference())
	})

	t.Run("nil when unset", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, (&Config{}).RemoteDatasetReference())
	})
}

func TestRead_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := Read("/nonexistent/config.yaml")
	assert.Error(t, err)
}

func TestWrite_CreatesDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "nested", "config.yaml")

	cfg := &Config{Agent: AgentRef{Name: "a1"}}
	require.NoError(t, Write(path, cfg))
	assert.FileExists(t, path)
}

// ---------------------------------------------------------------------------
// AgentRef fields
// ---------------------------------------------------------------------------

func TestAgentRef_YAMLFields(t *testing.T) {
	t.Parallel()

	input := `
name: test-agent
kind: hosted
version: v5
model: gpt-4.1
`
	var ref AgentRef
	require.NoError(t, yaml.Unmarshal([]byte(input), &ref))

	assert.Equal(t, "test-agent", ref.Name)
	assert.Equal(t, agent_yaml.AgentKindHosted, ref.Kind)
	assert.Equal(t, "v5", ref.Version)
	assert.Equal(t, "gpt-4.1", ref.Model)
}

// ---------------------------------------------------------------------------
// DatasetRef fields
// ---------------------------------------------------------------------------

func TestDatasetRef_YAMLFields(t *testing.T) {
	t.Parallel()

	input := `
name: golden-data
version: v3
`
	var ref DatasetRef
	require.NoError(t, yaml.Unmarshal([]byte(input), &ref))

	assert.Equal(t, "golden-data", ref.Name)
	assert.Equal(t, "v3", ref.Version)
}

// ---------------------------------------------------------------------------
// Options fields
// ---------------------------------------------------------------------------

func TestOptions_YAMLFields(t *testing.T) {
	t.Parallel()

	input := `
eval_model: gpt-4.1
max_candidates: 10
optimization_model: gpt-4o
`
	var opts Options
	require.NoError(t, yaml.Unmarshal([]byte(input), &opts))

	assert.Equal(t, "gpt-4.1", opts.EvalModel)
	require.NotNil(t, opts.MaxCandidates)
	assert.Equal(t, 10, *opts.MaxCandidates)
	assert.Equal(t, "gpt-4o", opts.OptimizationModel)
}

// TestOptions_MaxCandidates verifies the max_candidates key populates
// MaxCandidates.
func TestOptions_MaxCandidates(t *testing.T) {
	t.Parallel()

	input := `
eval_model: gpt-4.1
max_candidates: 7
`
	var opts Options
	require.NoError(t, yaml.Unmarshal([]byte(input), &opts))

	require.NotNil(t, opts.MaxCandidates)
	assert.Equal(t, 7, *opts.MaxCandidates)
}

// TestOptions_MaxStalls verifies the max_stalls key populates MaxStalls.
func TestOptions_MaxStalls(t *testing.T) {
	t.Parallel()

	input := `
eval_model: gpt-4.1
max_stalls: 3
`
	var opts Options
	require.NoError(t, yaml.Unmarshal([]byte(input), &opts))

	require.NotNil(t, opts.MaxStalls)
	assert.Equal(t, 3, *opts.MaxStalls)
}

func TestOptions_OptimizationConfig_NativeYAML(t *testing.T) {
	t.Parallel()

	input := `
eval_model: gpt-4o
optimization_model: gpt-5.1
optimization_config:
  model_search_space:
    - gpt-4o
    - gpt-5
    - gpt-5.1
  model: gpt-4o
`
	var opts Options
	require.NoError(t, yaml.Unmarshal([]byte(input), &opts))

	assert.Equal(t, "gpt-4o", opts.EvalModel)
	assert.Equal(t, "gpt-5.1", opts.OptimizationModel)

	require.NotNil(t, opts.OptimizationConfig)

	// model_search_space should be a JSON array.
	assert.JSONEq(t, `["gpt-4o","gpt-5","gpt-5.1"]`, string(opts.OptimizationConfig["model_search_space"]))

	// model should be a JSON string.
	assert.JSONEq(t, `"gpt-4o"`, string(opts.OptimizationConfig["model"]))
}

func TestOptions_OptimizationConfig_QuotedJSON(t *testing.T) {
	t.Parallel()

	// Users may provide pre-encoded JSON strings in YAML (legacy format).
	// These should be stored as-is, not double-encoded.
	input := `
optimization_config:
  model_search_space: '["gpt-4o","gpt-5"]'
  model: '"gpt-4o"'
`
	var opts Options
	require.NoError(t, yaml.Unmarshal([]byte(input), &opts))

	require.NotNil(t, opts.OptimizationConfig)

	// model_search_space should be the JSON array, not a JSON-encoded string.
	assert.JSONEq(t, `["gpt-4o","gpt-5"]`, string(opts.OptimizationConfig["model_search_space"]))

	// model should be the JSON string, not double-quoted.
	assert.JSONEq(t, `"gpt-4o"`, string(opts.OptimizationConfig["model"]))
}

// TestOptions_EvaluatorInitializationParameters verifies initialization_parameters
// under an evaluator entry is parsed correctly.
func TestOptions_EvaluatorInitializationParameters(t *testing.T) {
	t.Parallel()

	input := `
name: test
agent:
  name: my-agent
evaluators:
  - name: builtin.regex_match
    version: "4"
    initialization_parameters:
      patterns:
        - "(?i)Answer:\\s*{{ground_truth}}"
`
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte(input), &cfg))

	require.Len(t, cfg.Evaluators, 1)
	ref := cfg.Evaluators[0]
	assert.Equal(t, "builtin.regex_match", ref.Name)
	require.NotNil(t, ref.InitializationParameters)
	patterns, ok := ref.InitializationParameters["patterns"]
	require.True(t, ok)
	assert.NotEmpty(t, patterns)
}

// TestOptions_EvaluatorInitializationParametersOmitted verifies
// InitializationParameters is nil when absent.
func TestOptions_EvaluatorInitializationParametersOmitted(t *testing.T) {
	t.Parallel()

	input := `
name: test
agent:
  name: my-agent
evaluators:
  - name: builtin.task_adherence
`
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte(input), &cfg))

	require.Len(t, cfg.Evaluators, 1)
	assert.Nil(t, cfg.Evaluators[0].InitializationParameters)
}
