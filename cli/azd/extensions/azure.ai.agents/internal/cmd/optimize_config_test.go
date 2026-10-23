// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/optconfig"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

func TestDatasetRefToAPI_LocalRows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		rows     string
		count    int
		errorMsg string
	}{
		{
			name:  "GA completion",
			rows:  `{"query":"question","ground_truth":"answer","test_case_id":"case-1"}`,
			count: 1,
		},
		{
			name:  "generated completion",
			rows:  `{"id":1,"description":"Test objective","query":"question","candidate_response":"Synthetic answer"}`,
			count: 1,
		},
		{
			name:  "explicit ground truth retained",
			rows:  `{"query":"question","candidate_response":"Synthetic answer","ground_truth":"Verified answer"}`,
			count: 1,
		},
		{
			name:  "string generation ID",
			rows:  `{"query":"question","id":"case-1"}`,
			count: 1,
		},
		{
			name: "GA conversation",
			rows: `{"test_case_id":"case-1","test_case_description":"Resolve a return","test_case_category":"support",` +
				`"simulation_configuration":{"max_num_turns":3,"conversation_repetitions":2,"desired_num_turns":1}}`,
			count: 1,
		},
		{
			name:  "arbitrary extra columns",
			rows:  `{"query":"question","id":1,"context":"Private metadata","metadata":{"label":"Private metadata"}}`,
			count: 1,
		},
		{
			name:  "existing response is not ground truth",
			rows:  `{"query":"question","response":"Existing response"}`,
			count: 1,
		},
		{
			name:  "empty and null fields retained",
			rows:  `{"query":"question","ground_truth":"","test_case_id":null,"simulation_configuration":null}`,
			count: 1,
		},
		{
			name:  "escaped column names retained",
			rows:  `{"query":"question","extra\u001b[31m":"Private metadata"}`,
			count: 1,
		},
		{
			name:  "extra nested field retained",
			rows:  `{"test_case_description":"Return","simulation_configuration":{"unsupported":true}}`,
			count: 1,
		},
		{
			name:     "malformed JSON rejected",
			rows:     `{"query":`,
			errorMsg: "invalid JSON line",
		},
		{
			name:     "empty file rejected",
			rows:     " \n\n",
			errorMsg: "is empty",
		},
		{
			name: "multiple rows and blank lines",
			rows: " \n {\"query\":\"first\",\"id\":9007199254740993} \n\n" +
				"{\"query\":\"second\",\"description\":\"Test objective\",\"candidate_response\":\"Synthetic answer\"}\n",
			count: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeTestFile(t, t.TempDir(), "dataset.jsonl", tt.rows)
			dataset, err := datasetRefToAPI(&optconfig.DatasetRef{LocalURI: path})
			if tt.errorMsg != "" {
				require.ErrorContains(t, err, tt.errorMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, optimize_api.DatasetTypeInline, dataset.Type)
			require.Len(t, dataset.TestCases, tt.count)
			var expected []json.RawMessage
			for line := range strings.SplitSeq(tt.rows, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					expected = append(expected, json.RawMessage(line))
				}
			}
			assert.Equal(t, expected, dataset.TestCases, "all row fields must be preserved")
			serialized, err := json.Marshal(dataset)
			require.NoError(t, err)
			expectedJSON, err := json.Marshal(expected)
			require.NoError(t, err)
			assert.JSONEq(t, `{"type":"inline","test_cases":`+string(expectedJSON)+`}`, string(serialized))
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tt.rows, string(content), "conversion must not modify the source dataset")
		})
	}
}

func TestToRequest_GeneratedValidationDataset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := defaultOptimizeConfig("agent")
	cfg.DatasetFile = writeTestFile(t, dir, "train.jsonl", `{"query":"train","id":1}`)
	cfg.ValidationDataset = &optconfig.DatasetRef{
		LocalURI: writeTestFile(t, dir, "validation.jsonl",
			`{"query":"validation","description":"Test objective",`+
				`"candidate_response":"Synthetic answer","ground_truth":"answer"}`),
	}
	cfg.Options.EvalModel = "judge"
	cfg.Options.OptimizationModel = "optimizer"
	cfg.Evaluators = optconfig.EvaluatorList{{Name: "builtin.task_completion"}}

	request, warnings, err := cfg.ToRequest()
	require.NoError(t, err)
	assert.Empty(t, warnings)
	evaluation := request.OptimizationConfiguration.EvaluationConfiguration
	require.NotNil(t, evaluation.ValidationSet)
	require.Len(t, evaluation.ValidationSet.Source.TestCases, 1)
	assert.JSONEq(t, `{"query":"validation","description":"Test objective",`+
		`"candidate_response":"Synthetic answer","ground_truth":"answer"}`,
		string(evaluation.ValidationSet.Source.TestCases[0]))
}

func TestLoadOptimizeConfig_WithDatasetFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	yamlContent := `
target_configuration:
  type: foundry_agent
  name: my-agent
  version: "1"
optimization_model_configuration:
  model: gpt-5
optimization_configuration:
  type: agent_optimization
  evaluation_configuration:
    training_set:
      type: target_completion
      source:
        type: inline
        test_cases:
          - query: What is 2+2?
            ground_truth: "4"
          - query: Capital of France?
            ground_truth: Paris
    evaluators:
      - name: coherence
      - name: relevance
    evaluation_model:
      model: gpt-4o-mini
  candidate_search_configuration:
    max_candidates: 5
  agent_optimization_space:
    target_attributes: [instructions]
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	assert.Equal(t, "my-agent", req.TargetConfiguration.Name)
	assert.Equal(t, "1", req.TargetConfiguration.Version)
	require.NotNil(t, req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source)
	assert.Equal(t, optimize_api.DatasetTypeInline,
		req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.Type)
	assert.Len(t, req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.TestCases, 2)
	assert.JSONEq(t, `{"query":"What is 2+2?","ground_truth":"4"}`,
		string(req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.TestCases[0]))
	assert.Equal(t, "gpt-4o-mini", req.OptimizationConfiguration.EvaluationConfiguration.EvaluationModel.Model)
	assert.Equal(t, []optimize_api.EvaluatorRef{{Name: "coherence"}, {Name: "relevance"}},
		req.OptimizationConfiguration.EvaluationConfiguration.Evaluators)
}

func TestLoadOptimizeConfig_WithDataset(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	yamlContent := `
target_configuration:
  type: foundry_agent
  name: ref-agent
optimization_model_configuration:
  model: gpt-5
optimization_configuration:
  type: agent_optimization
  evaluation_configuration:
    training_set:
      type: target_completion
      source: {type: dataset_reference, name: my-dataset, version: "2"}
    validation_set:
      type: target_completion
      source: {type: dataset_reference, name: val-dataset, version: "1"}
    evaluators: [{name: builtin.task_adherence}]
    evaluation_model: {model: gpt-4o-mini}
  candidate_search_configuration: {max_candidates: 5}
  agent_optimization_space: {target_attributes: [instructions]}
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	assert.Equal(t, "ref-agent", req.TargetConfiguration.Name)
	require.NotNil(t, req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source)
	assert.Equal(t, optimize_api.DatasetTypeReference,
		req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.Type)
	assert.Empty(t, req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.TestCases)
	assert.Equal(t, "my-dataset", req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.Name)
	assert.Equal(t, "2", req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.Version)
	require.NotNil(t, req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source)
	assert.Equal(t, optimize_api.DatasetTypeReference,
		req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.Type)
	assert.Equal(t, "val-dataset", req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.Name)
}

// TestLoadOptimizeConfig_ValidationDataset verifies the GA validation_set
// key is honored.
func TestLoadOptimizeConfig_ValidationDataset(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	yamlContent := `
target_configuration:
  type: foundry_agent
  name: ref-agent
optimization_model_configuration: {model: gpt-5}
optimization_configuration:
  type: agent_optimization
  evaluation_configuration:
    training_set:
      type: target_completion
      source: {type: dataset_reference, name: my-dataset, version: "2"}
    validation_set:
      type: target_completion
      source: {type: dataset_reference, name: val-dataset, version: "3"}
    evaluators: [{name: builtin.task_adherence}]
    evaluation_model: {model: gpt-4o-mini}
  candidate_search_configuration: {max_candidates: 5}
  agent_optimization_space: {target_attributes: [instructions]}
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	require.NotNil(t, req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source)
	assert.Equal(t, "val-dataset", req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.Name)
	assert.Equal(t, "3", req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.Version)
}

// TestLoadOptimizeConfig_ValidationDataset_Inline verifies a local validation
// dataset (local_uri) is sent inline with its JSONL items.
func TestLoadOptimizeConfig_ValidationDataset_Inline(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	yamlContent := `
target_configuration:
  type: foundry_agent
  name: ref-agent
optimization_model_configuration: {model: gpt-5}
optimization_configuration:
  type: agent_optimization
  evaluation_configuration:
    training_set:
      type: target_completion
      source: {type: dataset_reference, name: my-dataset, version: "2"}
    validation_set:
      type: target_completion
      source:
        type: inline
        test_cases: [{query: "Capital of Japan?", ground_truth: Tokyo}]
    evaluators: [{name: builtin.task_adherence}]
    evaluation_model: {model: gpt-4o-mini}
  candidate_search_configuration: {max_candidates: 5}
  agent_optimization_space: {target_attributes: [instructions]}
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	require.NotNil(t, req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source)
	assert.Equal(t, optimize_api.DatasetTypeInline,
		req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.Type)
	require.Len(t, req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.TestCases, 1)
	assert.JSONEq(t, `{"query":"Capital of Japan?","ground_truth":"Tokyo"}`,
		string(req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.TestCases[0]))
	assert.Empty(t, req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.Name)
}

// TestLoadOptimizeConfig_RelativeLocalURI verifies that relative local_uri
// paths in dataset and validation_dataset are resolved against the agent
// project directory (simulating what submitJob does before calling ToRequest).
func TestLoadOptimizeConfig_RelativeLocalURI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create dataset files in a subdirectory of the project directory.
	sub := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(sub, 0750))
	writeTestFile(t, sub, "train.jsonl",
		`{"query":"hello","ground_truth":"hi"}
`)
	writeTestFile(t, sub, "val.jsonl",
		`{"query":"bye","ground_truth":"goodbye"}
`)

	cfg := defaultOptimizeConfig("rel-agent")
	cfg.Dataset = &optconfig.DatasetRef{LocalURI: filepath.Join("data", "train.jsonl")}
	cfg.ValidationDataset = &optconfig.DatasetRef{LocalURI: filepath.Join("data", "val.jsonl")}
	cfg.Evaluators = optconfig.EvaluatorList{{Name: "builtin.task_adherence"}}
	cfg.Options.EvalModel = "gpt-4o-mini"
	cfg.Options.OptimizationModel = "gpt-5"
	require.NoError(t, cfg.Validate())

	// Simulate submitJob path resolution: resolve relative paths against
	// the agent project directory (dir).
	agentProject := dir
	if cfg.Dataset.IsLocal() && !filepath.IsAbs(cfg.Dataset.LocalURI) {
		cfg.Dataset.LocalURI = filepath.Join(agentProject, cfg.Dataset.LocalURI)
	}
	if cfg.ValidationDataset.IsLocal() && !filepath.IsAbs(cfg.ValidationDataset.LocalURI) {
		cfg.ValidationDataset.LocalURI = filepath.Join(agentProject, cfg.ValidationDataset.LocalURI)
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	require.NotNil(t, req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source)
	assert.Equal(t, optimize_api.DatasetTypeInline,
		req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.Type)
	require.Len(t, req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.TestCases, 1)
	assert.JSONEq(t, `{"query":"hello","ground_truth":"hi"}`,
		string(req.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.TestCases[0]))

	require.NotNil(t, req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source)
	assert.Equal(t, optimize_api.DatasetTypeInline,
		req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.Type)
	require.Len(t, req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.TestCases, 1)
	assert.JSONEq(t, `{"query":"bye","ground_truth":"goodbye"}`,
		string(req.OptimizationConfiguration.EvaluationConfiguration.ValidationSet.Source.TestCases[0]))
}

func TestValidate_MissingAgentName(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Dataset: &optconfig.DatasetRef{Name: "ds", Version: "1"},
		},
		Options: &optconfig.Options{EvalModel: "gpt-4o-mini"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.name is required")
}

func TestValidate_MissingEvalModel(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:   optconfig.AgentRef{Name: "agent"},
			Dataset: &optconfig.DatasetRef{Name: "ds", Version: "1"},
		},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval_model is required")
}

func TestValidate_MissingOptimizationModel(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:   optconfig.AgentRef{Name: "agent"},
			Dataset: &optconfig.DatasetRef{Name: "ds", Version: "1"},
		},
		Options: &optconfig.Options{EvalModel: "gpt-4o-mini"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "optimization_model is required")
}

func TestValidate_MissingEvaluators(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:   optconfig.AgentRef{Name: "agent"},
			Dataset: &optconfig.DatasetRef{Name: "ds", Version: "1"},
		},
		Options: &optconfig.Options{EvalModel: "gpt-4o-mini", OptimizationModel: "gpt-5"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one evaluator is required")
}

func TestValidate_DatasetFileTakesPrecedence(t *testing.T) {
	t.Parallel()

	// dataset_file is the deprecated local form; it remains valid for backward
	// compatibility and takes precedence over a registered dataset_reference.
	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			DatasetFile: "tasks.jsonl",
			Dataset:     &optconfig.DatasetRef{Name: "ds", Version: "1"},
			Evaluators:  optconfig.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &optconfig.Options{EvalModel: "gpt-4o-mini", OptimizationModel: "gpt-5"},
	}

	require.NoError(t, cfg.Validate())
	assert.Equal(t, "tasks.jsonl", cfg.LocalDatasetPath())
}

func TestValidate_NeitherDatasetFileNorReference(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:      optconfig.AgentRef{Name: "agent"},
			Evaluators: optconfig.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &optconfig.Options{EvalModel: "gpt-4o-mini", OptimizationModel: "gpt-5"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a dataset is required")
}

func TestValidate_MaxStallsZeroIsRejected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			Evaluators:  optconfig.EvaluatorList{{Name: "builtin.task_adherence"}},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{
			EvalModel:         "gpt-4o-mini",
			OptimizationModel: "gpt-5",
			MaxStalls:         new(0),
		},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_stalls must be >= 1")
}

// TestValidate_MaxStallsNegativeIsRejected verifies Validate() rejects a negative
// max_stalls value set via the YAML config.
func TestValidate_MaxStallsNegativeIsRejected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			Evaluators:  optconfig.EvaluatorList{{Name: "builtin.task_adherence"}},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{
			EvalModel:         "gpt-4o-mini",
			OptimizationModel: "gpt-5",
			MaxStalls:         new(-1),
		},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_stalls must be >= 1")
}

func TestValidate_MaxStallsPositiveIsAccepted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			Evaluators:  optconfig.EvaluatorList{{Name: "builtin.task_adherence"}},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{
			EvalModel:         "gpt-4o-mini",
			OptimizationModel: "gpt-5",
			MaxStalls:         new(3),
		},
	}

	err := cfg.Validate()
	require.NoError(t, err)
}

func TestValidate_MaxConcurrentAgentRunsZeroIsRejected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			Evaluators:  optconfig.EvaluatorList{{Name: "builtin.task_adherence"}},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{
			EvalModel:              "gpt-4o-mini",
			OptimizationModel:      "gpt-5",
			MaxConcurrentAgentRuns: new(0),
		},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_concurrent_agent_runs must be >= 1")
}

func TestValidate_MaxConcurrentAgentRunsPositiveIsAccepted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			Evaluators:  optconfig.EvaluatorList{{Name: "builtin.task_adherence"}},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{
			EvalModel:              "gpt-4o-mini",
			OptimizationModel:      "gpt-5",
			MaxConcurrentAgentRuns: new(8),
		},
	}

	err := cfg.Validate()
	require.NoError(t, err)
}

func TestToRequest_MaxConcurrentAgentRunsForwardedToAPI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			Evaluators:  optconfig.EvaluatorList{{Name: "builtin.task_adherence"}},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{
			EvalModel:              "gpt-4o-mini",
			OptimizationModel:      "gpt-5",
			MaxConcurrentAgentRuns: new(8),
		},
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)
	require.NotNil(t, req.OptimizationConfiguration.EvaluationConfiguration.MaxConcurrentAgentRuns)
	assert.Equal(t, 8, *req.OptimizationConfiguration.EvaluationConfiguration.MaxConcurrentAgentRuns)
}

func TestToRequest_MaxConcurrentAgentRunsNilWhenOmitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			Evaluators:  optconfig.EvaluatorList{{Name: "builtin.task_adherence"}},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{
			EvalModel:         "gpt-4o-mini",
			OptimizationModel: "gpt-5",
		},
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)
	assert.Nil(t, req.OptimizationConfiguration.EvaluationConfiguration.MaxConcurrentAgentRuns)
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

	// The removed legacy input must not be accepted or silently reinterpreted.
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
  - builtin.task_adherence
options:
  eval_model: gpt-4o
`
	cfgPath := writeTestFile(t, dir, "eval.yaml", yamlContent)

	_, err := LoadOptimizeConfig(cfgPath)
	require.ErrorContains(t, err, "legacy")
}

func TestLoadOptimizeConfig_ScalarEvaluatorsWithOptions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	yamlContent := `
agent:
  name: my-test-agent

dataset_file: eval.jsonl

evaluators:
  - builtin.task_adherence

options:
  eval_model: gpt-4o
  budget: 3
`
	datasetPath := writeTestFile(t, dir, "eval.jsonl",
		`{"query":"hello","ground_truth":"hi"}
`)
	// Rewrite dataset_file to the real temp path so Validate+ToRequest work.
	yamlContent = `
agent:
  name: my-test-agent
dataset_file: ` + datasetPath + `
evaluators:
  - builtin.task_adherence
options:
  eval_model: gpt-4o
  budget: 3
  optimization_model: gpt-5
`
	cfgPath := writeTestFile(t, dir, "spec.yaml", yamlContent)

	_, err := LoadOptimizeConfig(cfgPath)
	require.ErrorContains(t, err, "legacy")
}

// ---------------------------------------------------------------------------
// parseSkillFile / loadSkillsFromDir
// ---------------------------------------------------------------------------

func TestParseSkillFile_MarkdownWithPreamble(t *testing.T) {
	t.Parallel()
	content := `---
name: policy-reviewer
description: Reviews a travel request against company travel policy.
---

# Policy Reviewer Skill

Review travel requests and provide a friendly assessment.
`
	skill := parseSkillFile("SKILL.md", content)
	assert.Equal(t, "policy-reviewer", skill.Name)
	assert.Equal(t, "Reviews a travel request against company travel policy.", skill.Description)
	assert.Contains(t, skill.Body, "# Policy Reviewer Skill")
	assert.Contains(t, skill.Body, "friendly assessment")
	assert.NotContains(t, skill.Body, "---")
}

func TestParseSkillFile_MarkdownWithoutPreamble(t *testing.T) {
	t.Parallel()
	content := "# Simple Skill\n\nDo something useful.\n"
	skill := parseSkillFile("simple.md", content)
	assert.Equal(t, "simple", skill.Name)
	assert.Empty(t, skill.Description)
	assert.Equal(t, content, skill.Body)
}

func TestParseSkillFile_NonMarkdown(t *testing.T) {
	t.Parallel()
	content := "You are a helpful assistant."
	skill := parseSkillFile("assistant.txt", content)
	assert.Equal(t, "assistant", skill.Name)
	assert.Empty(t, skill.Description)
	assert.Equal(t, content, skill.Body)
}

func TestParseSkillFile_PreambleNameOnly(t *testing.T) {
	t.Parallel()
	content := "---\nname: custom-name\n---\nBody content here.\n"
	skill := parseSkillFile("ignored-filename.md", content)
	assert.Equal(t, "custom-name", skill.Name)
	assert.Empty(t, skill.Description)
	assert.Equal(t, "Body content here.", skill.Body)
}

func TestLoadSkillsFromDir_WithMarkdownSkills(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	md := "---\nname: reviewer\ndescription: Reviews things\n---\n\nReview body.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0600))

	txt := "Plain text skill body."
	require.NoError(t, os.WriteFile(filepath.Join(dir, "helper.txt"), []byte(txt), 0600))

	skills, err := loadSkillsFromDir(dir)
	require.NoError(t, err)
	require.Len(t, skills, 2)

	// Find each skill by name.
	var mdSkill, txtSkill *optimize_api.SkillDefinition
	for i := range skills {
		switch skills[i].Name {
		case "reviewer":
			mdSkill = &skills[i]
		case "helper":
			txtSkill = &skills[i]
		}
	}

	require.NotNil(t, mdSkill)
	assert.Equal(t, "Reviews things", mdSkill.Description)
	assert.Contains(t, mdSkill.Body, "Review body.")

	require.NotNil(t, txtSkill)
	assert.Empty(t, txtSkill.Description)
	assert.Equal(t, txt, txtSkill.Body)
}

// ---------------------------------------------------------------------------
// loadToolDefinitions
// ---------------------------------------------------------------------------

func TestLoadToolDefinitions_Valid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	content := `[
  {
    "type": "function",
    "function": {
      "name": "get_weather",
      "description": "Get current weather for a location",
      "parameters": {
        "type": "object",
        "properties": {
          "location": {"type": "string", "description": "City name"}
        },
        "required": ["location"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "search",
      "description": "Search the web"
    }
  }
]`
	path := writeTestFile(t, dir, "tools.json", content)

	tools, _, err := loadToolDefinitions(path)
	require.NoError(t, err)
	require.Len(t, tools, 2)

	assert.Equal(t, "function", tools[0].Type)
	assert.Equal(t, "get_weather", tools[0].Function.Name)
	assert.Equal(t, "Get current weather for a location", tools[0].Function.Description)
	assert.NotNil(t, tools[0].Function.Parameters)

	assert.Equal(t, "function", tools[1].Type)
	assert.Equal(t, "search", tools[1].Function.Name)
}

func TestLoadToolDefinitions_FileNotFound(t *testing.T) {
	t.Parallel()
	_, _, err := loadToolDefinitions(filepath.Join(t.TempDir(), "nonexistent.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading tools file")
}

func TestLoadToolDefinitions_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTestFile(t, dir, "tools.json", "not json")

	_, _, err := loadToolDefinitions(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing tools file")
}

func TestLoadToolDefinitions_EmptyArray(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTestFile(t, dir, "tools.json", "[]")

	tools, warns, err := loadToolDefinitions(path)
	require.NoError(t, err)
	assert.Empty(t, tools)
	assert.Len(t, warns, 1)
	assert.Contains(t, warns[0], "no tool definitions")
}

func TestToRequest_WithToolsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	toolsContent := `[{"type":"function","function":{"name":"calculator","description":"Do math"}}]`
	toolsPath := writeTestFile(t, dir, "tools.json", toolsContent)

	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "test-agent"},
			DatasetFile: writeTestFile(t, dir, "dataset.jsonl", `{"query":"test"}`),
		},
		Options: &optconfig.Options{
			EvalModel: "gpt-4o",
		},
		ToolsFile: toolsPath,
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)
	require.NotNil(t, req.OptimizationConfiguration.BaselineAgentConfiguration)
	tools := req.OptimizationConfiguration.BaselineAgentConfiguration.Tools
	require.Len(t, tools, 1)
	assert.Equal(t, "calculator", tools[0].Function.Name)
}

// ---- ToRequest: baseline model in OptimizationConfig ----

func TestToRequest_SetsBaselineModelInOptimizationConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent", Model: "gpt-4o"},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{EvalModel: "gpt-4o-mini"},
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	require.NotNil(t, req.OptimizationConfiguration.BaselineAgentConfiguration)
	assert.Equal(t, "gpt-4o", req.OptimizationConfiguration.BaselineAgentConfiguration.CurrentModel)
}

func TestToRequest_BaselineModelOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{EvalModel: "gpt-4o-mini"},
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	assert.Nil(t, req.OptimizationConfiguration.BaselineAgentConfiguration)
}

func TestToRequest_BaselineModelInJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent", Model: "gpt-4o"},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{EvalModel: "gpt-4o-mini"},
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	// Verify the JSON output contains the model key inside optimization_config.
	data, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"model"`)
}

// ---------------------------------------------------------------------------
// evaluatorRefs — preserves name + version
// ---------------------------------------------------------------------------

func TestEvaluatorRefs(t *testing.T) {
	t.Parallel()

	t.Run("nil list returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, evaluatorRefs(nil))
	})

	t.Run("empty list returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, evaluatorRefs(optconfig.EvaluatorList{}))
	})

	t.Run("preserves name and version", func(t *testing.T) {
		t.Parallel()
		list := optconfig.EvaluatorList{
			{Name: "builtin.task_adherence"},
			{Name: "custom-quality", Version: "2", LocalURI: "evaluators/custom-quality_2.json"},
		}
		got := evaluatorRefs(list)
		require.Len(t, got, 2)
		assert.Equal(t, optimize_api.EvaluatorRef{Name: "builtin.task_adherence"}, got[0])
		// local_uri is not part of the wire EvaluatorRef — only name + version.
		assert.Equal(t, optimize_api.EvaluatorRef{Name: "custom-quality", Version: "2"}, got[1])
	})
}

// ---------------------------------------------------------------------------
// mergeEvaluators
// ---------------------------------------------------------------------------

func TestMergeEvaluators(t *testing.T) {
	t.Parallel()

	t.Run("appends new and dedups by name", func(t *testing.T) {
		t.Parallel()
		base := optconfig.EvaluatorList{{Name: "a"}, {Name: "b"}}
		add := optconfig.EvaluatorList{{Name: "b"}, {Name: "c"}}
		got := mergeEvaluators(base, add)
		require.Len(t, got, 3)
		assert.Equal(t, "a", got[0].Name)
		assert.Equal(t, "b", got[1].Name)
		assert.Equal(t, "c", got[2].Name)
	})

	t.Run("empty base returns add", func(t *testing.T) {
		t.Parallel()
		got := mergeEvaluators(nil, optconfig.EvaluatorList{{Name: "x"}})
		require.Len(t, got, 1)
		assert.Equal(t, "x", got[0].Name)
	})

	t.Run("empty add returns base", func(t *testing.T) {
		t.Parallel()
		base := optconfig.EvaluatorList{{Name: "x"}}
		got := mergeEvaluators(base, nil)
		require.Len(t, got, 1)
		assert.Equal(t, "x", got[0].Name)
	})
}

// ---------------------------------------------------------------------------
// loadSkillsFromDir — empty file handling
// ---------------------------------------------------------------------------

func TestLoadSkillsFromDir_SkipsEmptyFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Completely empty file — should be skipped.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty.md"), []byte(""), 0600))

	// Valid skill — should be included.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "valid.md"), []byte("# Real skill\nDoes things."), 0600))

	skills, err := loadSkillsFromDir(dir)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	assert.Equal(t, "valid", skills[0].Name)
}

func TestLoadSkillsFromDir_SkipsPreambleOnlyEmptyBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Preamble with name+description but no body — description is non-empty, should be kept.
	md := "---\nname: meta-only\ndescription: Has description\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.md"), []byte(md), 0600))

	skills, err := loadSkillsFromDir(dir)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	assert.Equal(t, "meta-only", skills[0].Name)
	assert.Equal(t, "Has description", skills[0].Description)
}

func TestLoadSkillsFromDir_AllEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte(""), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte(""), 0600))

	skills, err := loadSkillsFromDir(dir)
	require.NoError(t, err)
	assert.Empty(t, skills)
}

func TestToRequest_MaxStalls(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{
			EvalModel: "gpt-4o-mini",
			MaxStalls: new(3),
		},
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)
	require.NotNil(t, req.OptimizationConfiguration.CandidateSearchConfiguration.MaxConsecutiveFailedCandidateScreens)
	assert.Equal(t, 3, *req.OptimizationConfiguration.CandidateSearchConfiguration.MaxConsecutiveFailedCandidateScreens)
}

func TestToRequest_EvaluatorInitParamsForwardedToAPI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent: optconfig.AgentRef{Name: "agent"},
			Evaluators: optconfig.EvaluatorList{
				{
					Name:    "builtin.regex_match",
					Version: "4",
					InitializationParameters: map[string]any{
						"patterns": []any{"(?i)Answer:\\s*{{ground_truth}}"},
					},
				},
			},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi","ground_truth":"yes"}`),
		},
		Options: &optconfig.Options{
			EvalModel:         "gpt-4o-mini",
			OptimizationModel: "gpt-5",
		},
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)
	require.Len(t, req.OptimizationConfiguration.EvaluationConfiguration.Evaluators, 1)
	params := req.OptimizationConfiguration.EvaluationConfiguration.Evaluators[0].InitializationParameters
	require.NotNil(t, params, "expected initialization_parameters on evaluator ref")
	patterns, ok := params["patterns"]
	require.True(t, ok, "expected patterns key")
	assert.NotEmpty(t, patterns)
}

func TestToRequest_EvaluatorInitParamsNilWhenAbsent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &OptimizeConfig{
		Config: optconfig.Config{
			Agent:       optconfig.AgentRef{Name: "agent"},
			Evaluators:  optconfig.EvaluatorList{{Name: "builtin.task_adherence"}},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &optconfig.Options{
			EvalModel:         "gpt-4o-mini",
			OptimizationModel: "gpt-5",
		},
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)
	require.Len(t, req.OptimizationConfiguration.EvaluationConfiguration.Evaluators, 1)
	assert.Nil(t, req.OptimizationConfiguration.EvaluationConfiguration.Evaluators[0].InitializationParameters)
}
