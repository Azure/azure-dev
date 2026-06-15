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
		`{"query":"What is 2+2?","ground_truth":"4"}
{"query":"Capital of France?","ground_truth":"Paris"}
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
  max_candidates: 5
  optimization_model: gpt-5
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	assert.Equal(t, "my-agent", req.Agent.AgentName)
	assert.Equal(t, "1", req.Agent.AgentVersion)
	require.NotNil(t, req.TrainDataset)
	assert.Equal(t, optimize_api.DatasetTypeInline, req.TrainDataset.Type)
	assert.Len(t, req.TrainDataset.Items, 2)
	assert.Contains(t, string(req.TrainDataset.Items[0]), `"What is 2+2?"`)
	assert.Contains(t, string(req.TrainDataset.Items[0]), `"ground_truth"`)
	assert.Equal(t, "gpt-4o-mini", req.Options.EvalModel)
	assert.Equal(t, []optimize_api.EvaluatorRef{{Name: "coherence"}, {Name: "relevance"}}, req.Evaluators)
}

func TestLoadOptimizeConfig_WithDataset(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Uses the deprecated validation_reference key to verify backward compatibility.
	yamlContent := `
agent:
  name: ref-agent
dataset:
  name: my-dataset
  version: "2"
validation_reference:
  name: val-dataset
  version: "1"
evaluators:
  - builtin.task_adherence
options:
  eval_model: gpt-4o-mini
  optimization_model: gpt-5
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	assert.Equal(t, "ref-agent", req.Agent.AgentName)
	require.NotNil(t, req.TrainDataset)
	assert.Equal(t, optimize_api.DatasetTypeReference, req.TrainDataset.Type)
	assert.Empty(t, req.TrainDataset.Items)
	assert.Equal(t, "my-dataset", req.TrainDataset.Name)
	assert.Equal(t, "2", req.TrainDataset.Version)
	require.NotNil(t, req.ValidationDataset)
	assert.Equal(t, optimize_api.DatasetTypeReference, req.ValidationDataset.Type)
	assert.Equal(t, "val-dataset", req.ValidationDataset.Name)
}

// TestLoadOptimizeConfig_ValidationDataset verifies the new validation_dataset
// key is honored.
func TestLoadOptimizeConfig_ValidationDataset(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	yamlContent := `
agent:
  name: ref-agent
dataset:
  name: my-dataset
  version: "2"
validation_dataset:
  name: val-dataset
  version: "3"
evaluators:
  - builtin.task_adherence
options:
  eval_model: gpt-4o-mini
  optimization_model: gpt-5
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	require.NotNil(t, req.ValidationDataset)
	assert.Equal(t, "val-dataset", req.ValidationDataset.Name)
	assert.Equal(t, "3", req.ValidationDataset.Version)
}

// TestLoadOptimizeConfig_ValidationDataset_Inline verifies a local validation
// dataset (local_uri) is sent inline with its JSONL items.
func TestLoadOptimizeConfig_ValidationDataset_Inline(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	valPath := writeTestFile(t, dir, "val.jsonl",
		`{"query":"Capital of Japan?","ground_truth":"Tokyo"}
`)

	yamlContent := `
agent:
  name: ref-agent
dataset:
  name: my-dataset
  version: "2"
validation_dataset:
  local_uri: ` + valPath + `
evaluators:
  - builtin.task_adherence
options:
  eval_model: gpt-4o-mini
  optimization_model: gpt-5
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	require.NotNil(t, req.ValidationDataset)
	assert.Equal(t, optimize_api.DatasetTypeInline, req.ValidationDataset.Type)
	require.Len(t, req.ValidationDataset.Items, 1)
	assert.Contains(t, string(req.ValidationDataset.Items[0]), `"Tokyo"`)
	assert.Empty(t, req.ValidationDataset.Name)
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

	yamlContent := `
agent:
  name: rel-agent
dataset:
  local_uri: data/train.jsonl
validation_dataset:
  local_uri: data/val.jsonl
evaluators:
  - builtin.task_adherence
options:
  eval_model: gpt-4o-mini
  optimization_model: gpt-5
`
	cfgPath := writeTestFile(t, dir, "optimize.yaml", yamlContent)

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)
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

	require.NotNil(t, req.TrainDataset)
	assert.Equal(t, optimize_api.DatasetTypeInline, req.TrainDataset.Type)
	require.Len(t, req.TrainDataset.Items, 1)
	assert.Contains(t, string(req.TrainDataset.Items[0]), `"hello"`)

	require.NotNil(t, req.ValidationDataset)
	assert.Equal(t, optimize_api.DatasetTypeInline, req.ValidationDataset.Type)
	require.Len(t, req.ValidationDataset.Items, 1)
	assert.Contains(t, string(req.ValidationDataset.Items[0]), `"bye"`)
}

func TestValidate_MissingAgentName(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Dataset: &opt_eval.DatasetRef{Name: "ds", Version: "1"},
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
			Agent:   opt_eval.AgentRef{Name: "agent"},
			Dataset: &opt_eval.DatasetRef{Name: "ds", Version: "1"},
		},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval_model is required")
}

func TestValidate_MissingOptimizationModel(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:   opt_eval.AgentRef{Name: "agent"},
			Dataset: &opt_eval.DatasetRef{Name: "ds", Version: "1"},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o-mini"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "optimization_model is required")
}

func TestValidate_MissingEvaluators(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:   opt_eval.AgentRef{Name: "agent"},
			Dataset: &opt_eval.DatasetRef{Name: "ds", Version: "1"},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o-mini", OptimizationModel: "gpt-5"},
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
		Config: opt_eval.Config{
			Agent:       opt_eval.AgentRef{Name: "agent"},
			DatasetFile: "tasks.jsonl",
			Dataset:     &opt_eval.DatasetRef{Name: "ds", Version: "1"},
			Evaluators:  opt_eval.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o-mini", OptimizationModel: "gpt-5"},
	}

	require.NoError(t, cfg.Validate())
	assert.Equal(t, "tasks.jsonl", cfg.LocalDatasetPath())
}

func TestValidate_NeitherDatasetFileNorReference(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:      opt_eval.AgentRef{Name: "agent"},
			Evaluators: opt_eval.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o-mini", OptimizationModel: "gpt-5"},
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
	require.NotNil(t, cfg.Dataset)
	assert.Equal(t, "eval-dataset", cfg.Dataset.Name)
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

	cfg, err := LoadOptimizeConfig(cfgPath)
	require.NoError(t, err)

	// Agent
	assert.Equal(t, "my-test-agent", cfg.Agent.Name)

	// Dataset
	assert.Equal(t, datasetPath, cfg.DatasetFile)
	assert.Nil(t, cfg.Dataset)

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
	require.NotNil(t, req.TrainDataset)
	assert.Equal(t, optimize_api.DatasetTypeInline, req.TrainDataset.Type)
	assert.Len(t, req.TrainDataset.Items, 1)
	assert.Equal(t, []optimize_api.EvaluatorRef{{Name: "builtin.task_adherence"}}, req.Evaluators)
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
			DatasetFile: writeTestFile(t, dir, "dataset.jsonl", `{"query":"test"}`),
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

// ---- ToRequest: baseline model in OptimizationConfig ----

func TestToRequest_SetsBaselineModelInOptimizationConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:       opt_eval.AgentRef{Name: "agent", Model: "gpt-4o"},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o-mini"},
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	require.NotNil(t, req.Options.OptimizationConfig)
	raw, ok := req.Options.OptimizationConfig["model"]
	require.True(t, ok, "baseline model should be in optimization_config under the model key")
	assert.Equal(t, `"gpt-4o"`, string(raw))
}

func TestToRequest_BaselineModelOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:       opt_eval.AgentRef{Name: "agent"},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o-mini"},
	}

	req, _, err := cfg.ToRequest()
	require.NoError(t, err)

	if req.Options.OptimizationConfig != nil {
		_, hasKey := req.Options.OptimizationConfig["model"]
		assert.False(t, hasKey, "model should not be set when baseline model is empty")
	}
}

func TestToRequest_BaselineModelInJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:       opt_eval.AgentRef{Name: "agent", Model: "gpt-4o"},
			DatasetFile: writeTestFile(t, dir, "ds.jsonl", `{"query":"hi"}`),
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o-mini"},
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
		assert.Nil(t, evaluatorRefs(opt_eval.EvaluatorList{}))
	})

	t.Run("preserves name and version", func(t *testing.T) {
		t.Parallel()
		list := opt_eval.EvaluatorList{
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
		base := opt_eval.EvaluatorList{{Name: "a"}, {Name: "b"}}
		add := opt_eval.EvaluatorList{{Name: "b"}, {Name: "c"}}
		got := mergeEvaluators(base, add)
		require.Len(t, got, 3)
		assert.Equal(t, "a", got[0].Name)
		assert.Equal(t, "b", got[1].Name)
		assert.Equal(t, "c", got[2].Name)
	})

	t.Run("empty base returns add", func(t *testing.T) {
		t.Parallel()
		got := mergeEvaluators(nil, opt_eval.EvaluatorList{{Name: "x"}})
		require.Len(t, got, 1)
		assert.Equal(t, "x", got[0].Name)
	})

	t.Run("empty add returns base", func(t *testing.T) {
		t.Parallel()
		base := opt_eval.EvaluatorList{{Name: "x"}}
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
