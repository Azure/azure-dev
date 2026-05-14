// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package opteval

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
		Evaluators:  []string{"builtin.quality", "custom-1"},
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
	assert.Equal(t, "builtin.quality", loaded.Evaluators[0])
	assert.Equal(t, "custom-1", loaded.Evaluators[1])
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
kind: prompt
version: v5
model: gpt-4.1
`
	var ref AgentRef
	require.NoError(t, yaml.Unmarshal([]byte(input), &ref))

	assert.Equal(t, "test-agent", ref.Name)
	assert.Equal(t, agent_yaml.AgentKindPrompt, ref.Kind)
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
mode: full
strategies:
  - prompt
  - tool
budget: 500
max_iterations: 10
min_improvement: 0.05
improvement_threshold: 0.1
pass_threshold: 0.8
keep_versions: true
tasks_per_iteration: 20
reflection_model: gpt-4o
`
	var opts Options
	require.NoError(t, yaml.Unmarshal([]byte(input), &opts))

	assert.Equal(t, "gpt-4.1", opts.EvalModel)
	assert.Equal(t, "full", opts.Mode)
	assert.Equal(t, []string{"prompt", "tool"}, opts.Strategies)
	assert.Equal(t, 500, opts.Budget)
	assert.Equal(t, 10, opts.MaxIterations)
	assert.InDelta(t, 0.05, opts.MinImprovement, 0.001)
	assert.InDelta(t, 0.1, opts.ImprovementThreshold, 0.001)
	assert.InDelta(t, 0.8, opts.PassThreshold, 0.001)
	assert.True(t, opts.KeepVersions)
	assert.Equal(t, 20, opts.TasksPerIteration)
	assert.Equal(t, "gpt-4o", opts.ReflectionModel)
}
