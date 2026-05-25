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
		Agent:            AgentRef{Name: "a1"},
		DatasetReference: &DatasetRef{Name: "golden", Version: "v2"},
	}

	require.NoError(t, Write(path, original))
	loaded, err := Read(path)
	require.NoError(t, err)

	require.NotNil(t, loaded.DatasetReference)
	assert.Equal(t, "golden", loaded.DatasetReference.Name)
	assert.Equal(t, "v2", loaded.DatasetReference.Version)
	assert.Empty(t, loaded.DatasetFile)
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
max_iterations: 10
optimization_model: gpt-4o
`
	var opts Options
	require.NoError(t, yaml.Unmarshal([]byte(input), &opts))

	assert.Equal(t, "gpt-4.1", opts.EvalModel)
	require.NotNil(t, opts.MaxIterations)
	assert.Equal(t, 10, *opts.MaxIterations)
	assert.Equal(t, "gpt-4o", opts.OptimizationModel)
}

func TestOptions_LegacyTargetConfigBackwardCompat(t *testing.T) {
	t.Parallel()

	input := `
eval_model: gpt-4.1
target_config:
  model:
    - gpt-4o
    - gpt-5
`
	var opts Options
	require.NoError(t, yaml.Unmarshal([]byte(input), &opts))

	require.NotNil(t, opts.OptimizationConfig)
	require.Contains(t, opts.OptimizationConfig, "model")
}
