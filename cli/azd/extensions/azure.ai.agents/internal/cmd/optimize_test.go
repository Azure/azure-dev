// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/opt_eval"
	"azureaiagent/internal/pkg/agents/optimize_api"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestResolveOptimizeAgent_ServiceDefinition(t *testing.T) {
	tests := []struct {
		name        string
		properties  map[string]any
		referenced  bool
		staleLegacy bool
		wantPrompt  bool
	}{
		{"prompt", map[string]any{"kind": "prompt"}, false, false, true},
		{"hosted", map[string]any{"kind": "hosted", "name": "hosted-agent"}, false, false, false},
		{
			"voice",
			map[string]any{
				"kind": "voice",
				"name": "voice-wrapper",
				"conversationEngine": map[string]any{
					"type": "hosted_agent", "name": "voice-target", "version": "deployed",
				},
			},
			false,
			false,
			false,
		},
		{
			"prompt voice",
			map[string]any{
				"kind": "prompt-voice", "name": "voice-agent", "modelType": "managed",
				"model": map[string]any{"id": "gpt-realtime"},
			},
			false,
			false,
			false,
		},
		{"referenced prompt", map[string]any{"kind": "prompt"}, true, false, true},
		{"referenced hosted", map[string]any{"kind": "hosted", "name": "hosted-agent"}, true, false, false},
		{
			"hosted with stale legacy file",
			map[string]any{"kind": "hosted", "name": "hosted-agent"},
			false,
			true,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			props, err := structpb.NewStruct(tt.properties)
			require.NoError(t, err)
			svc := &azdext.ServiceConfig{Name: "assistant", Host: AiAgentHost, AdditionalProperties: props}
			if tt.referenced {
				definition := "kind: " + tt.properties["kind"].(string) + "\n"
				if name, ok := tt.properties["name"].(string); ok {
					definition += "name: " + name + "\n"
				}
				require.NoError(t, os.WriteFile(
					filepath.Join(root, "definition.yaml"),
					[]byte(definition),
					0o600,
				))
				svc.AdditionalProperties, err = structpb.NewStruct(map[string]any{"$ref": "definition.yaml"})
				require.NoError(t, err)
			}
			if tt.staleLegacy {
				require.NoError(t, os.WriteFile(
					filepath.Join(root, "agent.yaml"),
					[]byte("not: [valid"),
					0o600,
				))
			}
			server := &recordingProjectServer{
				projectPath: root,
				existing:    map[string]*azdext.ServiceConfig{"assistant": svc},
			}
			envServer := &testEnvironmentServiceServer{
				environments: map[string]*azdext.Environment{"dev": {Name: "dev"}},
				values: map[string]map[string]string{
					"dev": {"AGENT_ASSISTANT_NAME": "deployed-agent", "AGENT_ASSISTANT_VERSION": "2"},
				},
			}
			t.Setenv("AZD_SERVER", newProjectRecorderServer(t, server, envServer))
			t.Setenv("AGENT_DEFINITION_PATH", "")

			resolved, err := resolveOptimizeAgent(t.Context(), "assistant", "dev", true)
			require.NoError(t, err)
			require.Equal(t, tt.wantPrompt, resolved.promptAgent)
			require.Equal(t, "deployed-agent", resolved.agentName)
			require.Equal(t, "2", resolved.agentVersion)
			require.Equal(t, "assistant", resolved.serviceName)
			cfg := &OptimizeConfig{
				Config: opt_eval.Config{Agent: opt_eval.AgentRef{Name: resolved.agentName}},
				Options: &opt_eval.Options{OptimizationConfig: opt_eval.OptimizationConfig{
					"model":              json.RawMessage(`"gpt-5"`),
					"system_prompt":      json.RawMessage(`"Be helpful."`),
					"tools":              json.RawMessage(`[]`),
					"skills":             json.RawMessage(`[]`),
					"model_search_space": json.RawMessage(`["gpt-5"]`),
				}},
			}
			request, _, err := optimizeRequestConfig(cfg, resolved.promptAgent).ToRequest()
			require.NoError(t, err)
			for _, key := range []string{"model", "system_prompt", "tools", "skills"} {
				if tt.wantPrompt {
					require.NotContains(t, request.Options.OptimizationConfig, key)
				} else {
					require.Contains(t, request.Options.OptimizationConfig, key)
				}
			}
			require.Contains(t, request.Options.OptimizationConfig, "model_search_space")
			require.Len(t, cfg.Options.OptimizationConfig, 5)
		})
	}
}

func TestResolveOptimizeAgent_RejectsUnsupportedProjectDefinition(t *testing.T) {
	missingSuggestion := "add the direct agent definition to the azure.ai.agent service in azure.yaml, " +
		"or add a service-level $ref to a direct agent definition"
	legacySuggestion := "move the direct agent definition into the azure.ai.agent service in azure.yaml, " +
		"or move any env, project, language, image, or docker fields onto the service before adding " +
		"a service-level $ref to the remaining direct definition"
	nestedSuggestion := "move the agent definition to service-level properties in azure.yaml, " +
		"or add a service-level $ref to a direct agent definition"
	overrideSuggestion := "unset AGENT_DEFINITION_PATH, then move the agent definition to " +
		"the azure.ai.agent service in azure.yaml, or add a service-level $ref to a direct agent definition"

	tests := []struct {
		name           string
		definitionPath string
		setup          func(*testing.T, string) *azdext.ServiceConfig
		wantCode       string
		wantSuggestion string
		wantMessage    string
	}{
		{
			name: "implicit agent yaml",
			setup: func(t *testing.T, root string) *azdext.ServiceConfig {
				require.NoError(t, os.WriteFile(
					filepath.Join(root, "agent.yaml"),
					[]byte("not: [valid"),
					0o600,
				))
				return &azdext.ServiceConfig{Name: "assistant", Host: AiAgentHost, RelativePath: "."}
			},
			wantCode:       exterrors.CodeAgentDefinitionNotFound,
			wantSuggestion: legacySuggestion,
			wantMessage:    "found legacy file agent.yaml",
		},
		{
			name: "missing definition",
			setup: func(*testing.T, string) *azdext.ServiceConfig {
				return &azdext.ServiceConfig{Name: "assistant", Host: AiAgentHost}
			},
			wantCode:       exterrors.CodeAgentDefinitionNotFound,
			wantSuggestion: missingSuggestion,
			wantMessage:    `agent definition not found for service "assistant"`,
		},
		{
			name: "nested config",
			setup: func(t *testing.T, _ string) *azdext.ServiceConfig {
				config, err := structpb.NewStruct(map[string]any{"kind": "hosted"})
				require.NoError(t, err)
				return &azdext.ServiceConfig{Name: "assistant", Host: AiAgentHost, Config: config}
			},
			wantCode:       exterrors.CodeDeprecatedAgentServiceConfig,
			wantSuggestion: nestedSuggestion,
			wantMessage:    `service "assistant" uses the unsupported nested config block`,
		},
		{
			name:           "definition path",
			definitionPath: "missing.yaml",
			setup:          optimizeTestDirectService,
			wantCode:       exterrors.CodeUnsupportedAgentDefinitionPath,
			wantSuggestion: overrideSuggestion,
			wantMessage:    "AGENT_DEFINITION_PATH is no longer supported",
		},
		{
			name:           "whitespace definition path",
			definitionPath: "   ",
			setup:          optimizeTestDirectService,
			wantCode:       exterrors.CodeUnsupportedAgentDefinitionPath,
			wantSuggestion: overrideSuggestion,
			wantMessage:    "AGENT_DEFINITION_PATH is no longer supported",
		},
		{
			name: "missing root ref",
			setup: func(t *testing.T, _ string) *azdext.ServiceConfig {
				props, err := structpb.NewStruct(map[string]any{"$ref": "missing.yaml"})
				require.NoError(t, err)
				return &azdext.ServiceConfig{Name: "assistant", Host: AiAgentHost, AdditionalProperties: props}
			},
			wantCode:    foundry.CodeInvalidFileRef,
			wantMessage: "missing.yaml",
		},
		{
			name: "malformed root ref",
			setup: func(t *testing.T, root string) *azdext.ServiceConfig {
				require.NoError(t, os.WriteFile(
					filepath.Join(root, "definition.yaml"),
					[]byte("kind: ["),
					0o600,
				))
				props, err := structpb.NewStruct(map[string]any{"$ref": "definition.yaml"})
				require.NoError(t, err)
				return &azdext.ServiceConfig{Name: "assistant", Host: AiAgentHost, AdditionalProperties: props}
			},
			wantCode:    foundry.CodeInvalidFileRef,
			wantMessage: "definition.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AGENT_DEFINITION_PATH", tt.definitionPath)
			root := t.TempDir()
			svc := tt.setup(t, root)
			envServer := &testEnvironmentServiceServer{
				environments: map[string]*azdext.Environment{"dev": {Name: "dev"}},
				values: map[string]map[string]string{
					"dev": {"AGENT_ASSISTANT_NAME": "stale-deployed-agent"},
				},
			}
			server := &recordingProjectServer{
				projectPath: root,
				existing:    map[string]*azdext.ServiceConfig{"assistant": svc},
			}
			t.Setenv("AZD_SERVER", newProjectRecorderServer(t, server, envServer))

			_, _, _, authoritativeErr := projectpkg.LoadAgentDefinition(svc, root)
			require.Error(t, authoritativeErr)
			resolved, err := resolveOptimizeAgent(t.Context(), "assistant", "dev", true)
			require.Nil(t, resolved)
			require.Equal(t, authoritativeErr, err, "optimize must return the authoritative source error unchanged")

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, tt.wantCode, localErr.Code)
			if tt.wantSuggestion != "" {
				require.Equal(t, tt.wantSuggestion, localErr.Suggestion)
			}
			require.ErrorContains(t, err, tt.wantMessage)
		})
	}
}

func TestResolveOptimizeAgent_NoProjectUsesStandaloneAgentName(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "")
	t.Setenv("AZD_SERVER", newProjectRecorderServer(t, &recordingProjectServer{nilProject: true}))

	resolved, err := resolveOptimizeAgent(t.Context(), "remote-agent", "dev", true)
	require.NoError(t, err)
	require.Equal(t, &optimizeAgentContext{agentName: "remote-agent"}, resolved)
}

func optimizeTestDirectService(t *testing.T, _ string) *azdext.ServiceConfig {
	t.Helper()
	props, err := structpb.NewStruct(map[string]any{"kind": "hosted", "name": "hosted-agent"})
	require.NoError(t, err)
	return &azdext.ServiceConfig{Name: "assistant", Host: AiAgentHost, AdditionalProperties: props}
}

func TestOptimizeCommand_HasExpectedSubCommands(t *testing.T) {
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	expected := []string{"status", "list", "cancel", "deploy", "apply"}
	var actual []string
	for _, sub := range cmd.Commands() {
		actual = append(actual, sub.Name())
	}

	for _, name := range expected {
		assert.Contains(t, actual, name, "optimize should have sub-command %q", name)
	}
	assert.NotContains(t, actual, "run", "optimize should not have 'run' sub-command (merged into root)")
}

func TestOptimizeCommand_AcceptsPositionalArg(t *testing.T) {
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	err := cmd.Args(cmd, []string{"my-agent"})
	assert.NoError(t, err)

	err = cmd.Args(cmd, []string{})
	assert.NoError(t, err)

	err = cmd.Args(cmd, []string{"my-agent", "extra"})
	assert.Error(t, err)
}

func TestOptimizeCommand_AcceptsConfigFlag(t *testing.T) {
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	f := cmd.Flags().Lookup("config")
	require.NotNil(t, f, "--config flag should be registered")
	assert.Equal(t, "c", f.Shorthand, "--config should have -c shorthand")

	assert.NotNil(t, cmd.Flags().Lookup("poll-interval"))
}

func TestOptimizeCommand_DatasetFlag(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	f := cmd.Flags().Lookup("dataset")
	require.NotNil(t, f, "--dataset flag should be registered")
	assert.Equal(t, "d", f.Shorthand, "--dataset should have -d shorthand")
	assert.Equal(t, "", f.DefValue, "--dataset default should be empty")
}

func TestOptimizeCommand_DefaultFlags(t *testing.T) {
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	pollVal, err := cmd.Flags().GetInt("poll-interval")
	require.NoError(t, err)
	assert.Equal(t, 10, pollVal, "--poll-interval should default to 10")
}

func TestIsTerminal_ViaOptimizeAPI(t *testing.T) {
	assert.True(t, optimize_api.IsTerminal(optimize_api.StatusCompleted))
	assert.True(t, optimize_api.IsTerminal(optimize_api.StatusFailed))
	assert.True(t, optimize_api.IsTerminal(optimize_api.StatusCancelled))
	assert.False(t, optimize_api.IsTerminal(optimize_api.StatusRunning))
	assert.False(t, optimize_api.IsTerminal(optimize_api.StatusPending))
	assert.False(t, optimize_api.IsTerminal(""))
}

func TestTruncateString(t *testing.T) {
	assert.Equal(t, "abc", truncateString("abc", 10))
	assert.Equal(t, "abcdefg...", truncateString("abcdefghijk", 10))
	assert.Equal(t, "ab", truncateString("abcdef", 2))
}

func TestFormatOptimizeStatus(t *testing.T) {
	assert.NotEmpty(t, formatOptimizeStatus(optimize_api.StatusCompleted))
	assert.NotEmpty(t, formatOptimizeStatus(optimize_api.StatusFailed))
	assert.NotEmpty(t, formatOptimizeStatus(optimize_api.StatusCancelled))
	assert.NotEmpty(t, formatOptimizeStatus(optimize_api.StatusRunning))
	assert.NotEmpty(t, formatOptimizeStatus("unknown"))
}

// ---- defaultOptimizeConfig ----

func TestDefaultOptimizeConfig(t *testing.T) {
	t.Parallel()
	cfg := defaultOptimizeConfig("my-agent")

	assert.Equal(t, "my-agent", cfg.Agent.Name)
	require.NotNil(t, cfg.Options)
	assert.Empty(t, cfg.Options.EvalModel)
	// No default evaluator is set; it must come from config or --evaluator.
	assert.Empty(t, cfg.Evaluators)
}

// ---- LoadOptimizeConfig + reconcileConfigAgent (--config path) ----

func TestLoadOptimizeConfig_ReconcileAgent(t *testing.T) {
	t.Parallel()

	writeConfigYAML := func(t *testing.T, dir, agentName string) string {
		t.Helper()
		content := "agent:\n  name: " + agentName + "\noptions:\n  eval_model: gpt-4o\n  mode: optimize\n"
		path := filepath.Join(dir, "spec.yaml")
		require.NoError(t, os.WriteFile(path, []byte(content), 0600))
		return path
	}

	t.Run("env overrides config when names differ", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := writeConfigYAML(t, dir, "config-agent")

		cfg, err := LoadOptimizeConfig(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, "config-agent", cfg.Agent.Name)

		changed := reconcileConfigAgent(io.Discard, &cfg.Agent, "env-agent", "", cfgPath)
		assert.True(t, changed, "should report change when names differ")
		assert.Equal(t, "env-agent", cfg.Agent.Name, "environment name should take precedence")
	})

	t.Run("no change when names match", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := writeConfigYAML(t, dir, "same-agent")

		cfg, err := LoadOptimizeConfig(cfgPath)
		require.NoError(t, err)

		changed := reconcileConfigAgent(io.Discard, &cfg.Agent, "same-agent", "", cfgPath)
		assert.False(t, changed)
		assert.Equal(t, "same-agent", cfg.Agent.Name)
	})

	t.Run("sets name when config has empty agent name", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		content := "agent:\n  kind: hosted\noptions:\n  eval_model: gpt-4o\n"
		cfgPath := filepath.Join(dir, "spec.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0600))

		cfg, err := LoadOptimizeConfig(cfgPath)
		require.NoError(t, err)
		assert.Empty(t, cfg.Agent.Name)

		changed := reconcileConfigAgent(io.Discard, &cfg.Agent, "env-agent", "", cfgPath)
		assert.False(t, changed, "filling empty name is not a 'change' (no conflict)")
		assert.Equal(t, "env-agent", cfg.Agent.Name)
	})

	t.Run("no-op when env name is empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := writeConfigYAML(t, dir, "config-agent")

		cfg, err := LoadOptimizeConfig(cfgPath)
		require.NoError(t, err)

		changed := reconcileConfigAgent(io.Discard, &cfg.Agent, "", "", cfgPath)
		assert.False(t, changed)
		assert.Equal(t, "config-agent", cfg.Agent.Name, "original name preserved when env is empty")
	})
}

// ---- applyOverrides: --dataset flag ----

func TestApplyOverrides_DatasetFlag_LocalFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "data.jsonl")
	require.NoError(t, os.WriteFile(dataFile, []byte(`{"input":"hi"}`+"\n"), 0600))

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:      opt_eval.AgentRef{Name: "a", Instruction: opt_eval.InstructionRef{Value: "test"}},
			Evaluators: opt_eval.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o", OptimizationModel: "gpt-5"},
	}

	action := &OptimizeAction{
		flags:    &optimizeFlags{dataset: dataFile},
		noPrompt: true,
	}

	err := action.applyOverrides(t.Context(), cfg, dir)
	require.NoError(t, err)
	assert.Equal(t, dataFile, cfg.DatasetFile)
	assert.Nil(t, cfg.Dataset)
}

func TestApplyOverrides_DatasetFlag_RegisteredName(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:      opt_eval.AgentRef{Name: "a", Instruction: opt_eval.InstructionRef{Value: "test"}},
			Evaluators: opt_eval.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o", OptimizationModel: "gpt-5"},
	}

	action := &OptimizeAction{
		flags:    &optimizeFlags{dataset: "my-golden-dataset"},
		noPrompt: true,
	}

	err := action.applyOverrides(t.Context(), cfg, "")
	require.NoError(t, err)
	assert.Empty(t, cfg.DatasetFile)
	require.NotNil(t, cfg.Dataset)
	assert.Equal(t, "my-golden-dataset", cfg.Dataset.Name)
}

func TestApplyOverrides_DatasetFlag_OverridesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "new.jsonl")
	require.NoError(t, os.WriteFile(dataFile, []byte(`{"input":"hi"}`+"\n"), 0600))

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:      opt_eval.AgentRef{Name: "a", Instruction: opt_eval.InstructionRef{Value: "test"}},
			Dataset:    &opt_eval.DatasetRef{Name: "old-ref"},
			Evaluators: opt_eval.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o", OptimizationModel: "gpt-5"},
	}

	action := &OptimizeAction{
		flags:    &optimizeFlags{dataset: dataFile},
		noPrompt: true,
	}

	err := action.applyOverrides(t.Context(), cfg, dir)
	require.NoError(t, err)
	assert.Equal(t, dataFile, cfg.DatasetFile, "file should replace ref")
	assert.Nil(t, cfg.Dataset, "ref should be cleared")
}

// ---- eval.yaml auto-use in --no-prompt mode ----

func TestLoadOptimizeConfig_EvalYAML_WithDatasetFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Simulate an eval.yaml with dataset_file — the format generated by "azd ai agent eval generate".
	dataFile := filepath.Join(dir, "data.jsonl")
	require.NoError(t, os.WriteFile(dataFile, []byte(`{"input":"hello"}`+"\n"), 0600))

	evalYAML := fmt.Sprintf(`agent:
  name: travel-agent
dataset_file: %s
evaluators:
  - name: builtin.task_adherence
options:
  eval_model: gpt-4o
`, dataFile)
	evalPath := filepath.Join(dir, "eval.yaml")
	require.NoError(t, os.WriteFile(evalPath, []byte(evalYAML), 0600))

	cfg, err := LoadOptimizeConfig(evalPath)
	require.NoError(t, err)
	assert.Equal(t, "travel-agent", cfg.Agent.Name)
	assert.Equal(t, dataFile, cfg.DatasetFile, "dataset_file from eval.yaml should be loaded")
	assert.Nil(t, cfg.Dataset)
	assert.Equal(t, "gpt-4o", cfg.Options.EvalModel)
}

func TestLoadOptimizeConfig_EvalYAML_WithDatasetReference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	evalYAML := `agent:
  name: travel-agent
dataset_reference:
  name: golden-dataset
  version: "2"
evaluators:
  - name: builtin.task_adherence
options:
  eval_model: gpt-4o
`
	evalPath := filepath.Join(dir, "eval.yaml")
	require.NoError(t, os.WriteFile(evalPath, []byte(evalYAML), 0600))

	cfg, err := LoadOptimizeConfig(evalPath)
	require.NoError(t, err)
	assert.Equal(t, "travel-agent", cfg.Agent.Name)
	assert.Empty(t, cfg.DatasetFile)
	require.NotNil(t, cfg.Dataset, "dataset_reference from eval.yaml should be loaded")
	assert.Equal(t, "golden-dataset", cfg.Dataset.Name)
	assert.Equal(t, "2", cfg.Dataset.Version)
}

func TestApplyOverrides_NoPrompt_EvalYAML_WithDataset_Succeeds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a real dataset file so validation passes.
	dataFile := filepath.Join(dir, "data.jsonl")
	require.NoError(t, os.WriteFile(dataFile, []byte(`{"input":"hi"}`+"\n"), 0600))

	// Config as if loaded from eval.yaml (has dataset already).
	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:       opt_eval.AgentRef{Name: "travel-agent", Instruction: opt_eval.InstructionRef{Value: "You are a travel agent."}},
			DatasetFile: dataFile,
			Evaluators:  opt_eval.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o", OptimizationModel: "gpt-5"},
	}

	action := &OptimizeAction{
		flags:    &optimizeFlags{},
		noPrompt: true,
	}

	err := action.applyOverrides(t.Context(), cfg, "")
	require.NoError(t, err)
	assert.Equal(t, dataFile, cfg.DatasetFile, "dataset from eval.yaml should be preserved")
}

func TestResolveOptimizeDataset_NoPrompt_ReturnsError(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{Name: "a"},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o"},
	}

	err := resolveOptimizeDataset(t.Context(), nil, cfg, "", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a dataset is required")
}

func TestApplyOverrides_NoPrompt_NoDataset_ReturnsError(t *testing.T) {
	t.Parallel()

	// Config without dataset — simulates using defaultOptimizeConfig in --no-prompt.
	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{Name: "a", Instruction: opt_eval.InstructionRef{Value: "test"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o"},
	}

	action := &OptimizeAction{
		flags:    &optimizeFlags{},
		noPrompt: true,
	}

	err := action.applyOverrides(t.Context(), cfg, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a dataset is required")
}

func TestApplyOverrides_PromptAgentUsesServiceSideDefinition(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{Name: "prompt-agent"},
			Dataset: &opt_eval.DatasetRef{
				Name: "optimization-dataset",
			},
			Evaluators: opt_eval.EvaluatorList{
				{Name: "builtin.task_adherence"},
			},
		},
		Options: &opt_eval.Options{
			EvalModel:         "gpt-4.1-mini",
			OptimizationModel: "gpt-5",
		},
	}
	action := &OptimizeAction{
		flags:       &optimizeFlags{},
		noPrompt:    true,
		promptAgent: true,
	}

	require.NoError(t, action.applyOverrides(t.Context(), cfg, t.TempDir()))
	require.True(t, cfg.Agent.Instruction.IsEmpty())
	require.Empty(t, cfg.Agent.Model)
	require.Empty(t, cfg.SkillDir)
	require.Empty(t, cfg.ToolsFile)
	require.Nil(t, cfg.Options.OptimizationConfig)

	request, _, err := cfg.ToRequest()
	require.NoError(t, err)
	require.Nil(t, request.Options.OptimizationConfig)
}

func TestOptimizeRequestConfig_PromptAgentOmitsServiceSideDefinition(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{
				Name:        "prompt-agent",
				Model:       "gpt-4.1-mini",
				Instruction: opt_eval.InstructionRef{File: "missing-instructions.md"},
			},
		},
		SkillDir:  "missing-skills",
		ToolsFile: "missing-tools.json",
		Options: &opt_eval.Options{
			OptimizationConfig: opt_eval.OptimizationConfig{
				"model":              json.RawMessage(`"gpt-4.1-mini"`),
				"system_prompt":      json.RawMessage(`"Be helpful."`),
				"skills":             json.RawMessage(`[{"name":"search"}]`),
				"tools":              json.RawMessage(`[{"type":"code_interpreter"}]`),
				"model_search_space": json.RawMessage(`["gpt-4.1-mini","gpt-5"]`),
			},
		},
	}

	requestConfig := optimizeRequestConfig(cfg, true)
	request, _, err := requestConfig.ToRequest()
	require.NoError(t, err)
	require.Equal(t, optimize_api.AgentIdentifier{AgentName: "prompt-agent"}, request.Agent)
	require.Equal(t, map[string]json.RawMessage{
		"model_search_space": json.RawMessage(`["gpt-4.1-mini","gpt-5"]`),
	}, request.Options.OptimizationConfig)

	require.Equal(t, "gpt-4.1-mini", cfg.Agent.Model)
	require.Equal(t, "missing-instructions.md", cfg.Agent.Instruction.File)
	require.Equal(t, "missing-skills", cfg.SkillDir)
	require.Equal(t, "missing-tools.json", cfg.ToolsFile)
	require.Len(t, cfg.Options.OptimizationConfig, 5)
}

func TestOptimizeRequestConfig_HostedAgentKeepsLocalDefinition(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{}
	require.Same(t, cfg, optimizeRequestConfig(cfg, false))
}

// ---- printOptimizeResults — table format ----

func TestPrintOptimizeResults_TableHasCandidateScorePass(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		Result: &optimize_api.OptimizeResult{
			Best: "candidate_1",
			Candidates: []optimize_api.CandidateResult{
				{Name: "baseline", AvgScore: 0.91},
				{Name: "candidate_1", AvgScore: 0.95},
			},
		},
	}

	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, false, "")
	out := buf.String()

	// Verify header columns.
	assert.Contains(t, out, "Candidate")
	assert.Contains(t, out, "Score")

	// Verify no removed columns.
	assert.NotContains(t, out, "Strategies")
	assert.NotContains(t, out, "Tokens")
	assert.NotContains(t, out, "Optimal")
	assert.NotContains(t, out, "Pass")

	// Verify candidate data.
	assert.Contains(t, out, "baseline")
	assert.Contains(t, out, "candidate_1")
	assert.Contains(t, out, "0.91")
	assert.Contains(t, out, "0.95")
}

func TestPrintOptimizeResults_BestMarkedWithStar(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		Result: &optimize_api.OptimizeResult{
			Best: "candidate_1",
			Candidates: []optimize_api.CandidateResult{
				{Name: "baseline", AvgScore: 0.80},
				{Name: "candidate_1", AvgScore: 0.95},
			},
		},
	}

	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, false, "")

	assert.Contains(t, buf.String(), "candidate_1 ★")
}

func TestPrintOptimizeResults_NoCandidates(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{}
	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, false, "")

	// Should print nothing for an empty candidates list.
	assert.Empty(t, buf.String())
}

func TestPrintOptimizeResults_ShowsCandidateIDs(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		Result: &optimize_api.OptimizeResult{
			Best: "abc-123",
			Candidates: []optimize_api.CandidateResult{
				{Name: "candidate_1", AvgScore: 0.95, CandidateID: "abc-123"},
			},
		},
	}

	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, true, "")
	out := buf.String()

	assert.Contains(t, out, "Candidate IDs")
	assert.Contains(t, out, "abc-123")
	assert.Contains(t, out, "optimize apply")
}

func TestPrintOptimizeResults_ShowsStrategyColumn(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		Result: &optimize_api.OptimizeResult{
			Best: "candidate_1",
			Candidates: []optimize_api.CandidateResult{
				{Name: "baseline", AvgScore: 0.90},
				{
					Name:     "candidate_1",
					AvgScore: 0.95,
					Mutations: map[string]any{
						"skills": []any{
							map[string]any{
								"name":        "policy-reviewer",
								"description": "Reviews travel requests",
								"body":        "updated instructions",
							},
						},
						"system_prompt": "new prompt",
					},
				},
			},
		},
	}

	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, false, "")
	out := buf.String()

	// Strategy header and mutation keys (sorted) are shown.
	assert.Contains(t, out, "Strategy")
	assert.Contains(t, out, "skills")
}

func TestPrintOptimizeResults_NoStrategyColumnWhenNoMutations(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		Result: &optimize_api.OptimizeResult{
			Best: "candidate_1",
			Candidates: []optimize_api.CandidateResult{
				{Name: "candidate_1", AvgScore: 0.95},
			},
		},
	}

	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, false, "")

	assert.NotContains(t, buf.String(), "Strategy")
}

func TestPrintOptimizeResults_StrategyDashForMixedMutations(t *testing.T) {
	t.Parallel()

	// One candidate with mutations, one without — both are printed in the
	// Strategy column. The candidate without mutations should show "-".
	status := &optimize_api.OptimizeJobStatus{
		Result: &optimize_api.OptimizeResult{
			Best: "candidate_1",
			Candidates: []optimize_api.CandidateResult{
				{Name: "baseline", AvgScore: 0.80},
				{
					Name:      "candidate_1",
					AvgScore:  0.95,
					Mutations: map[string]any{"system_prompt": "new"},
				},
			},
		},
	}

	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, false, "")
	out := buf.String()

	assert.Contains(t, out, "Strategy")
	assert.Contains(t, out, "system_prompt")
	// The baseline row (no mutations) should contain a dash placeholder.
	lines := strings.SplitSeq(out, "\n")
	found := false
	for line := range lines {
		if strings.Contains(line, "baseline") && strings.Contains(line, "0.800") {
			found = true
			assert.Contains(t, line, "-", "baseline row should show dash for empty strategy")
		}
	}
	assert.True(t, found, "expected a baseline row with score 0.800 in output:\n%s", out)
}
