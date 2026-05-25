// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/opt_eval"
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
options:
  eval_model: gpt-4o-mini
  budget: 100
  max_iterations: 5
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	assert.Equal(t, "my-agent", req.Agent.AgentName)
	assert.Equal(t, "1", req.Agent.AgentVersion)
	assert.Len(t, req.Dataset, 2)
	assert.Contains(t, string(req.Dataset[0]), `"What is 2+2?"`)
	assert.Contains(t, string(req.Dataset[0]), `"groundTruth"`)
	assert.Nil(t, req.TrainDatasetReference)
	assert.Equal(t, "gpt-4o-mini", req.Options.EvalModel)
	assert.Equal(t, []string{"coherence", "relevance"}, req.Evaluators)
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

	req, _, err := cfg.ToRequest()
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
		Config: opt_eval.Config{
			DatasetReference: &opt_eval.DatasetRef{Name: "ds", Version: "1"},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o-mini"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.name is required")
}

func TestValidate_MissingEvalModel(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:            opt_eval.AgentRef{Name: "agent"},
			DatasetReference: &opt_eval.DatasetRef{Name: "ds", Version: "1"},
		},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval_model is required")
}

func TestValidate_BothDatasetFileAndReference(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:            opt_eval.AgentRef{Name: "agent"},
			DatasetFile:      "tasks.jsonl",
			DatasetReference: &opt_eval.DatasetRef{Name: "ds", Version: "1"},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o-mini"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestValidate_NeitherDatasetFileNorReference(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config:  opt_eval.Config{Agent: opt_eval.AgentRef{Name: "agent"}},
		Options: &opt_eval.Options{EvalModel: "gpt-4o-mini"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a dataset is required")
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
  - builtin.task_adherence
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
	assert.Equal(t, "builtin.task_adherence", cfg.Evaluators[0].Name)
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
  - builtin.task_adherence

options:
  eval_model: gpt-4o
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
  - builtin.task_adherence
options:
  eval_model: gpt-4o
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
	assert.Equal(t, "builtin.task_adherence", cfg.Evaluators[0].Name)

	// Options
	require.NotNil(t, cfg.Options)
	assert.Equal(t, "gpt-4o", cfg.Options.EvalModel)

	// Validate + ToRequest
	require.NoError(t, cfg.Validate())
	req, _, err := cfg.ToRequest()
	require.NoError(t, err)
	assert.Equal(t, "my-test-agent", req.Agent.AgentName)
	assert.Len(t, req.Dataset, 1)
	assert.Equal(t, []string{"builtin.task_adherence"}, req.Evaluators)
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
		Config: opt_eval.Config{
			Agent:       opt_eval.AgentRef{Name: "test-agent"},
			DatasetFile: writeTestFile(t, dir, "dataset.jsonl", `{"prompt":"test"}`),
		},
		Options: &opt_eval.Options{
			EvalModel: "gpt-4o",
		},
		ToolsFile: toolsPath,
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)
	require.Contains(t, req.Options.OptimizationConfig, "tools")
	// Verify tool definitions are serialized into optimization_config.
	var tools []optimize_api.ToolDefinition
	require.NoError(t, json.Unmarshal(req.Options.OptimizationConfig["tools"], &tools))
	require.Len(t, tools, 1)
	assert.Equal(t, "calculator", tools[0].Function.Name)
}
