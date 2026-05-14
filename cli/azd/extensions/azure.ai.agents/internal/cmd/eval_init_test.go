// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// newEvalInitCommand — command shape
// ---------------------------------------------------------------------------

func TestNewEvalInitCommand_Flags(t *testing.T) {
	t.Parallel()
	cmd := newEvalInitCommand(&azdext.ExtensionContext{})

	expectedFlags := []struct {
		name         string
		defaultValue string
	}{
		{"name", ""},
		{"no-wait", "false"},
		{"agent", ""},
		{"project-endpoint", ""},
		{"gen-instruction", ""},
		{"gen-instruction-file", ""},
		{"eval-model", defaultEvalModel},
		{"dataset", ""},
		{"max-samples", "100"},
		{"out-file", defaultEvalConfigName},
		{"reset-defaults", "false"},
	}

	for _, ef := range expectedFlags {
		t.Run(ef.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(ef.name)
			require.NotNil(t, f, "flag %q should exist", ef.name)
			assert.Equal(t, ef.defaultValue, f.DefValue)
		})
	}
}

func TestNewEvalInitCommand_NoArgs(t *testing.T) {
	t.Parallel()
	cmd := newEvalInitCommand(&azdext.ExtensionContext{})
	assert.NoError(t, cmd.Args(cmd, nil))
	assert.Error(t, cmd.Args(cmd, []string{"extra"}))
}

func TestNewEvalInitCommand_ShortOutFile(t *testing.T) {
	t.Parallel()
	cmd := newEvalInitCommand(&azdext.ExtensionContext{})
	f := cmd.Flags().ShorthandLookup("O")
	require.NotNil(t, f, "flag -O shorthand should exist")
	assert.Equal(t, "out-file", f.Name)
}

// ---------------------------------------------------------------------------
// gen-instruction / gen-instruction-file mutual exclusion
// ---------------------------------------------------------------------------

func TestRunEvalInit_MutualExclusion(t *testing.T) {
	t.Parallel()
	flags := &evalInitFlags{
		genInstruction:     "inline text",
		genInstructionFile: "some-file.txt",
	}
	err := runEvalInit(t.Context(), flags, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot use both --gen-instruction and --gen-instruction-file")
}

func TestRunEvalInit_InstructionFromFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	instrFile := filepath.Join(tmpDir, "instruction.md")
	require.NoError(t, os.WriteFile(instrFile, []byte("  Test booking agent  \n"), 0600))

	flags := &evalInitFlags{
		genInstructionFile: instrFile,
		evalModel:          defaultEvalModel,
		maxSamples:         10,
	}
	// runEvalInit will fail later (no azd client), but genInstruction should be populated first.
	_ = runEvalInit(t.Context(), flags, true)
	assert.Equal(t, "Test booking agent", flags.genInstruction)
}

func TestRunEvalInit_InstructionFileMissing(t *testing.T) {
	t.Parallel()
	flags := &evalInitFlags{
		genInstructionFile: "/nonexistent/path/instruction.txt",
	}
	err := runEvalInit(t.Context(), flags, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading instruction file")
}

// ---------------------------------------------------------------------------
// newEvalConfig
// ---------------------------------------------------------------------------

func TestNewEvalConfig(t *testing.T) {
	t.Parallel()

	t.Run("uses default name", func(t *testing.T) {
		t.Parallel()
		flags := &evalInitFlags{
			genInstruction: "Test the booking agent",
			evalModel:      "gpt-4.1",
			maxSamples:     50,
		}
		resolved := &evalResolvedContext{
			agentName: "booking-agent",
			agentKind: agent_yaml.AgentKindHosted,
			version:   "v2",
		}

		cfg := newEvalConfig(flags, resolved)

		assert.Equal(t, defaultEvalName, cfg.Name)
		assert.Equal(t, "booking-agent", cfg.Agent.Name)
		assert.Equal(t, agent_yaml.AgentKindHosted, cfg.Agent.Kind)
		assert.Equal(t, "v2", cfg.Agent.Version)
		assert.Equal(t, "gpt-4.1", cfg.Options.EvalModel)
		assert.Equal(t, "Test the booking agent", cfg.GenerationInstruction)
		assert.Equal(t, 50, cfg.MaxSamples)
	})

	t.Run("uses custom name from flag", func(t *testing.T) {
		t.Parallel()
		flags := &evalInitFlags{
			name:       "my-suite",
			maxSamples: 10,
		}
		resolved := &evalResolvedContext{agentName: "a"}
		cfg := newEvalConfig(flags, resolved)
		assert.Equal(t, "my-suite", cfg.Name)
	})
}

// ---------------------------------------------------------------------------
// datasetFromJob
// ---------------------------------------------------------------------------

func TestDatasetFromJob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		job             *eval_api.GenerationJob
		expectedName    string
		expectedVersion string
	}{
		{
			"standard fields",
			&eval_api.GenerationJob{DatasetName: "ds-1", DatasetVersion: "v2"},
			"ds-1", "v2",
		},
		{
			"name fallback",
			&eval_api.GenerationJob{Name: "ds-2"},
			"ds-2", "v1",
		},
		{
			"version fallback",
			&eval_api.GenerationJob{DatasetName: "ds-3", Version: "v3"},
			"ds-3", "v3",
		},
		{
			"empty defaults version to v1",
			&eval_api.GenerationJob{Name: "ds-4"},
			"ds-4", "v1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ref := datasetFromJob(tt.job)
			assert.Equal(t, tt.expectedName, ref.Name)
			assert.Equal(t, tt.expectedVersion, ref.Version)
		})
	}
}

// ---------------------------------------------------------------------------
// parseDatasetURI
// ---------------------------------------------------------------------------

func TestIsDatasetName(t *testing.T) {
	t.Parallel()

	t.Run("simple name is a dataset name", func(t *testing.T) {
		t.Parallel()
		assert.True(t, eval_api.IsDatasetName("eval-data-2026-04-16"))
	})

	t.Run("name with dots but no data extension", func(t *testing.T) {
		t.Parallel()
		assert.True(t, eval_api.IsDatasetName("my-dataset.v2"))
	})

	t.Run("jsonl file is not a name", func(t *testing.T) {
		t.Parallel()
		assert.False(t, eval_api.IsDatasetName("golden.jsonl"))
	})

	t.Run("json file is not a name", func(t *testing.T) {
		t.Parallel()
		assert.False(t, eval_api.IsDatasetName("data.json"))
	})

	t.Run("csv file is not a name", func(t *testing.T) {
		t.Parallel()
		assert.False(t, eval_api.IsDatasetName("results.csv"))
	})

	t.Run("path with separator is not a name", func(t *testing.T) {
		t.Parallel()
		assert.False(t, eval_api.IsDatasetName("./tests/golden.jsonl"))
	})

	t.Run("empty string is not a name", func(t *testing.T) {
		t.Parallel()
		assert.False(t, eval_api.IsDatasetName(""))
	})
}

// ---------------------------------------------------------------------------
// buildModelChoices
// ---------------------------------------------------------------------------

func TestBuildModelChoices(t *testing.T) {
	t.Parallel()

	t.Run("no deployed model has select-other only", func(t *testing.T) {
		t.Parallel()
		choices := buildModelChoices("")
		require.Len(t, choices, 1)
		assert.Equal(t, selectOtherDeployment, choices[0].Value)
		assert.Equal(t, "Select another deployment", choices[0].Label)
	})

	t.Run("deployed model first then select-other", func(t *testing.T) {
		t.Parallel()
		choices := buildModelChoices("my-deployment")
		require.Len(t, choices, 2)
		assert.Equal(t, "my-deployment", choices[0].Value)
		assert.Contains(t, choices[0].Label, "(deployed)")
		assert.Equal(t, selectOtherDeployment, choices[1].Value)
	})
}

// ---------------------------------------------------------------------------
// evaluatorFromJob
// ---------------------------------------------------------------------------

func TestEvaluatorFromJob(t *testing.T) {
	t.Parallel()

	t.Run("extracts name from job", func(t *testing.T) {
		t.Parallel()
		job := &eval_api.GenerationJob{
			EvaluatorName: "quality-eval",
		}
		name := evaluatorFromJob(job)
		assert.Equal(t, "quality-eval", name)
	})

	t.Run("extracts name from result", func(t *testing.T) {
		t.Parallel()
		job := &eval_api.GenerationJob{
			Result: json.RawMessage(`{"name":"smoke-core","display_name":"smoke-core"}`),
		}
		name := evaluatorFromJob(job)
		assert.Equal(t, "smoke-core", name)
	})

	t.Run("returns empty when no name", func(t *testing.T) {
		t.Parallel()
		job := &eval_api.GenerationJob{}
		name := evaluatorFromJob(job)
		assert.Empty(t, name)
	})
}

// ---------------------------------------------------------------------------
// eval_api.BuildGenerationSources
// ---------------------------------------------------------------------------

func TestBuildGenerationSources(t *testing.T) {
	t.Parallel()

	t.Run("hosted agent includes prompt and agent sources", func(t *testing.T) {
		t.Parallel()
		sources := eval_api.BuildGenerationSources(
			string(agent_yaml.AgentKindHosted), "my-agent", "v2",
			"Test customer service interactions", nil,
		)
		require.Len(t, sources, 2)

		// First source: prompt
		assert.Equal(t, "prompt", sources[0].Type)
		assert.Equal(t, "Test customer service interactions", sources[0].Prompt)

		// Second source: agent
		assert.Equal(t, "agent", sources[1].Type)
		assert.Equal(t, "my-agent", sources[1].AgentName)
		assert.Equal(t, "v2", sources[1].AgentVersion)
		assert.Empty(t, sources[1].Prompt)
	})

	t.Run("prompt agent includes only agent source", func(t *testing.T) {
		t.Parallel()
		sources := eval_api.BuildGenerationSources(
			string(agent_yaml.AgentKindPrompt), "prompt-agent", "v1", "", nil,
		)
		require.Len(t, sources, 1)

		assert.Equal(t, "agent", sources[0].Type)
		assert.Equal(t, "prompt-agent", sources[0].AgentName)
		assert.Equal(t, "v1", sources[0].AgentVersion)
		assert.Empty(t, sources[0].Prompt, "prompt agents should not have prompt field")
	})

	t.Run("prompt agent without version omits agent_version", func(t *testing.T) {
		t.Parallel()
		sources := eval_api.BuildGenerationSources(
			string(agent_yaml.AgentKindPrompt), "prompt-agent", "", "", nil,
		)
		require.Len(t, sources, 1)

		assert.Equal(t, "agent", sources[0].Type)
		assert.Equal(t, "prompt-agent", sources[0].AgentName)
		assert.Empty(t, sources[0].AgentVersion, "empty version should be omitted")
	})

	t.Run("hosted agent without instruction omits prompt source", func(t *testing.T) {
		t.Parallel()
		sources := eval_api.BuildGenerationSources(
			string(agent_yaml.AgentKindHosted), "my-agent", "v1", "", nil,
		)
		require.Len(t, sources, 1)
		assert.Equal(t, "agent", sources[0].Type)
	})
}

// ---------------------------------------------------------------------------
// evaluatorsFromFlags
// ---------------------------------------------------------------------------

func TestEvaluatorsFromFlags(t *testing.T) {
	t.Parallel()

	t.Run("passes through strings", func(t *testing.T) {
		t.Parallel()
		result := evaluatorsFromFlags([]string{"builtin.task_adherence", "my-custom"})
		require.Len(t, result, 2)
		assert.Equal(t, "builtin.task_adherence", result[0])
		assert.Equal(t, "my-custom", result[1])
	})

	t.Run("nil returns nil", func(t *testing.T) {
		t.Parallel()
		result := evaluatorsFromFlags(nil)
		assert.Nil(t, result)
	})
}

// ---------------------------------------------------------------------------
// buildOpenAIEvalRequest
// ---------------------------------------------------------------------------

func TestBuildOpenAIEvalRequest(t *testing.T) {
	t.Parallel()

	cfg := &evalConfig{
		Config: opteval.Config{
			Name: "smoke-core",
			Agent: evalAgentRef{
				Name:    "agent-1",
				Version: "v1",
			},
			DatasetReference: &evalDatasetRef{Name: "ds", Version: "v1"},
			Evaluators:       []string{"builtin.quality"},
		},
		Options: &opteval.Options{EvalModel: "gpt-4o"},
	}

	req := buildOpenAIEvalRequest(cfg)

	assert.Equal(t, "smoke-core", req.Name)
	assert.Equal(t, "agent-1", req.Metadata["azd_agent"])
	assert.Equal(t, "v1", req.Metadata["azd_agent_version"])
	require.NotNil(t, req.DataSourceConfig)
	assert.Equal(t, "custom", req.DataSourceConfig.Type)
	require.Len(t, req.TestingCriteria, 1)
	assert.Equal(t, "azure_ai_evaluator", req.TestingCriteria[0].Type)
	assert.Equal(t, "builtin.quality", req.TestingCriteria[0].EvaluatorName)
	assert.Equal(t, "gpt-4o", req.TestingCriteria[0].InitializationParameters["model"])
	assert.Equal(t, "{{item.messages}}", req.TestingCriteria[0].DataMapping["messages"])
	assert.Equal(t, "{{item.query}}", req.TestingCriteria[0].DataMapping["query"])
	assert.Equal(t, "{{sample.output_items}}", req.TestingCriteria[0].DataMapping["response"])
}

func TestBuildOpenAIEvalRequest_WithDatasetFile(t *testing.T) {
	t.Parallel()

	cfg := &evalConfig{
		Config: opteval.Config{
			Name:        "test-eval",
			Agent:       evalAgentRef{Name: "agent-1"},
			DatasetFile: "tasks.jsonl",
		},
	}

	req := buildOpenAIEvalRequest(cfg)
	require.NotNil(t, req.DataSourceConfig)
	assert.Equal(t, "custom", req.DataSourceConfig.Type)
	assert.Empty(t, req.TestingCriteria)
}

// ---------------------------------------------------------------------------
// resolveLocalDatasetFile
// ---------------------------------------------------------------------------

func TestResolveLocalDatasetFile_Absolute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "tasks.jsonl")
	require.NoError(t, os.WriteFile(f, []byte(`{"query":"hi"}`+"\n"), 0600))

	result, err := resolveLocalDatasetFile(f, "/other")
	require.NoError(t, err)
	assert.Equal(t, f, result)
}

func TestResolveLocalDatasetFile_Relative(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "data.jsonl")
	require.NoError(t, os.WriteFile(f, []byte(`{"query":"hi"}`+"\n"), 0600))

	result, err := resolveLocalDatasetFile("data.jsonl", dir)
	require.NoError(t, err)
	assert.Equal(t, f, result)
}

func TestResolveLocalDatasetFile_NotFound(t *testing.T) {
	t.Parallel()
	_, err := resolveLocalDatasetFile("missing.jsonl", t.TempDir())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not accessible")
}

// ---------------------------------------------------------------------------
// tryLoadExistingEvalConfig
// ---------------------------------------------------------------------------

func TestTryLoadExistingEvalConfig_Found(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "eval.yaml")
	cfg := &evalConfig{
		Config: opteval.Config{
			Name: "smoke-core",
			Agent: evalAgentRef{
				Name: "my-agent",
			},
			DatasetFile: "data.jsonl",
			Evaluators:  []string{"quality"},
		},
	}
	require.NoError(t, writeEvalConfig(cfgPath, cfg))

	loaded, ok := tryLoadExistingEvalConfig(cfgPath)
	require.True(t, ok)
	assert.Equal(t, "smoke-core", loaded.Name)
	assert.Equal(t, "my-agent", loaded.Agent.Name)
	assert.Equal(t, []string{"quality"}, loaded.Evaluators)
}

func TestTryLoadExistingEvalConfig_NotFound(t *testing.T) {
	t.Parallel()
	cfg, ok := tryLoadExistingEvalConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	assert.False(t, ok)
	assert.Nil(t, cfg)
}

func TestTryLoadExistingEvalConfig_InvalidYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "eval.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(":\ninvalid: [yaml"), 0600))

	cfg, ok := tryLoadExistingEvalConfig(cfgPath)
	assert.False(t, ok)
	assert.Nil(t, cfg)
}

// ---------------------------------------------------------------------------
// eval_api.SplitEvaluators / eval_api.IsBuiltinEvaluator
// ---------------------------------------------------------------------------

func TestIsBuiltinEvaluator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"builtin prefix", "builtin.task_adherence", true},
		{"builtin prefix dot only", "builtin.", true},
		{"custom evaluator", "my-quality", false},
		{"empty string", "", false},
		{"similar prefix", "builtins.quality", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, eval_api.IsBuiltinEvaluator(tt.input))
		})
	}
}

func TestSplitEvaluators(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		input             []string
		expectedGenerated []string
		expectedBuiltin   []string
	}{
		{
			"mixed list",
			[]string{"builtin.task_adherence", "my-quality", "builtin.safety"},
			[]string{"my-quality"},
			[]string{"builtin.task_adherence", "builtin.safety"},
		},
		{
			"all builtin",
			[]string{"builtin.quality", "builtin.safety"},
			nil,
			[]string{"builtin.quality", "builtin.safety"},
		},
		{
			"all generated",
			[]string{"smoke-core", "custom-1"},
			[]string{"smoke-core", "custom-1"},
			nil,
		},
		{
			"empty list",
			nil,
			nil,
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generated, builtin := eval_api.SplitEvaluators(tt.input)
			assert.Equal(t, tt.expectedGenerated, generated)
			assert.Equal(t, tt.expectedBuiltin, builtin)
		})
	}
}
