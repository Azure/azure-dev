// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

func TestLoadOptimizeConfig_WithDatasetFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	datasetPath := writeTestFile(t, dir, "tasks.jsonl",
		`{"prompt":"What is 2+2?","groundTruth":"4"}
{"prompt":"Capital of France?","groundTruth":"Paris"}
`)

	yamlContent := `
agent:
  name: my-agent
  version: "1"
  model: gpt-4o
dataset_file: ` + datasetPath + `
evaluators:
  - coherence
  - relevance
criteria:
  - name: accuracy
    instruction: answer must be correct
options:
  eval_model: gpt-4o-mini
  budget: 100
  max_iterations: 5
  strategies:
    - prompt_mutation
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, err := cfg.ToRequest("https://example.ai.azure.com/project/p")
	require.NoError(t, err)

	assert.Equal(t, "my-agent", req.Agent.AgentName)
	assert.Equal(t, "1", req.Agent.AgentVersion)
	assert.Equal(t, "https://example.ai.azure.com/project/p", req.Agent.FoundryProjectURL)
	assert.Len(t, req.Dataset, 2)
	assert.Equal(t, "What is 2+2?", req.Dataset[0].Prompt)
	assert.Equal(t, "4", req.Dataset[0].GroundTruth)
	assert.Nil(t, req.TrainDatasetReference)
	assert.Equal(t, "gpt-4o-mini", req.Options.EvalModel)
	assert.Equal(t, 100, req.Options.Budget)
	assert.Equal(t, []string{"coherence", "relevance"}, req.Evaluators)
	assert.Len(t, req.Criteria, 1)
	assert.Equal(t, "accuracy", req.Criteria[0].Name)
}

func TestLoadOptimizeConfig_WithDatasetReference(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	yamlContent := `
agent:
  name: ref-agent
dataset_reference:
  name: my-dataset
  version: "2"
validation_reference:
  name: val-dataset
  version: "1"
options:
  eval_model: gpt-4o-mini
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, err := cfg.ToRequest("https://example.com/proj")
	require.NoError(t, err)

	assert.Equal(t, "ref-agent", req.Agent.AgentName)
	assert.Empty(t, req.Dataset)
	require.NotNil(t, req.TrainDatasetReference)
	assert.Equal(t, "my-dataset", req.TrainDatasetReference.Name)
	assert.Equal(t, "2", req.TrainDatasetReference.Version)
	require.NotNil(t, req.ValidationDatasetReference)
	assert.Equal(t, "val-dataset", req.ValidationDatasetReference.Name)
}

func TestValidate_MissingAgentName(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opteval.Config{
			DatasetReference: &opteval.DatasetRef{Name: "ds", Version: "1"},
		},
		Options: &opteval.Options{EvalModel: "gpt-4o-mini"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.name is required")
}

func TestValidate_MissingEvalModel(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opteval.Config{
			Agent:            opteval.AgentRef{Name: "agent"},
			DatasetReference: &opteval.DatasetRef{Name: "ds", Version: "1"},
		},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval_model is required")
}

func TestValidate_BothDatasetFileAndReference(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opteval.Config{
			Agent:            opteval.AgentRef{Name: "agent"},
			DatasetFile:      "tasks.jsonl",
			DatasetReference: &opteval.DatasetRef{Name: "ds", Version: "1"},
		},
		Options: &opteval.Options{EvalModel: "gpt-4o-mini"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestValidate_NeitherDatasetFileNorReference(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config:  opteval.Config{Agent: opteval.AgentRef{Name: "agent"}},
		Options: &opteval.Options{EvalModel: "gpt-4o-mini"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one of dataset_file or dataset_reference is required")
}

func TestLoadOptimizeConfig_FileNotFound(t *testing.T) {
	t.Parallel()

	_, err := LoadOptimizeConfig("/nonexistent/path/optimize.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestLoadOptimizeConfig_InvalidYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := writeTestFile(t, dir, "bad.yaml", "{{invalid yaml}}")

	_, err := LoadOptimizeConfig(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse config")
}

func TestLoadOptimizeConfig_EvalYAMLFormat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// An eval.yaml file should be loadable by the optimize config loader.
	// eval_model at the top level won't map to Options, so we verify the
	// agent and evaluators parse correctly.
	yamlContent := `
name: smoke-core
agent:
  name: my-eval-agent
  version: "3"
  kind: hosted
dataset_reference:
  name: eval-dataset
  version: "1"
evaluators:
  - task_adherence
options:
  eval_model: gpt-4o
`
	cfgPath := writeTestFile(t, dir, "eval.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "my-eval-agent", cfg.Agent.Name)
	assert.Equal(t, "3", cfg.Agent.Version)
	require.NotNil(t, cfg.Options)
	assert.Equal(t, "gpt-4o", cfg.Options.EvalModel)
	assert.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "task_adherence", cfg.Evaluators[0])
	require.NotNil(t, cfg.DatasetReference)
	assert.Equal(t, "eval-dataset", cfg.DatasetReference.Name)
}

func TestLoadOptimizeConfig_ScalarEvaluatorsWithOptions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	yamlContent := `
agent:
  name: my-test-agent

dataset_file: eval.jsonl

evaluators:
  - task_adherence

options:
  eval_model: gpt-4o
  mode: evaluate
  strategies:
    - instruction
  budget: 3
`
	datasetPath := writeTestFile(t, dir, "eval.jsonl",
		`{"prompt":"hello","groundTruth":"hi"}
`)
	// Rewrite dataset_file to the real temp path so Validate+ToRequest work.
	yamlContent = `
agent:
  name: my-test-agent
dataset_file: ` + datasetPath + `
evaluators:
  - task_adherence
options:
  eval_model: gpt-4o
  mode: evaluate
  strategies:
    - instruction
  budget: 3
`
	cfgPath := writeTestFile(t, dir, "spec.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)

	// Agent
	assert.Equal(t, "my-test-agent", cfg.Agent.Name)

	// Dataset
	assert.Equal(t, datasetPath, cfg.DatasetFile)
	assert.Nil(t, cfg.DatasetReference)

	// Evaluator — scalar string without builtin. prefix resolves as custom.
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "task_adherence", cfg.Evaluators[0])

	// Options
	require.NotNil(t, cfg.Options)
	assert.Equal(t, "gpt-4o", cfg.Options.EvalModel)
	assert.Equal(t, "evaluate", cfg.Options.Mode)
	assert.Equal(t, []string{"instruction"}, cfg.Options.TargetAttributes)
	assert.Equal(t, 3, cfg.Options.Budget)

	// Validate + ToRequest
	require.NoError(t, cfg.Validate())
	req, err := cfg.ToRequest("https://example.ai.azure.com/project/p")
	require.NoError(t, err)
	assert.Equal(t, "my-test-agent", req.Agent.AgentName)
	assert.Len(t, req.Dataset, 1)
	assert.Equal(t, []string{"task_adherence"}, req.Evaluators)
}
