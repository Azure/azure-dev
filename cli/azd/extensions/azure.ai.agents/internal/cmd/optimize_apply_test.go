// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/opt_eval"
	"azureaiagent/internal/pkg/agents/optimize_api"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// ---- newOptimizeApplyCommand — command shape ----

func TestNewOptimizeApplyCommand_UseString(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeApplyCommand(&azdext.ExtensionContext{})
	assert.Equal(t, "apply", cmd.Use)
}

func TestNewOptimizeApplyCommand_Flags(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeApplyCommand(&azdext.ExtensionContext{})

	require.NotNil(t, cmd.Flags().Lookup("candidate"))
	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("endpoint"))
	require.NotNil(t, cmd.Flags().Lookup("project-endpoint"))
}

func TestNewOptimizeApplyCommand_CandidateIsRequired(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeApplyCommand(&azdext.ExtensionContext{})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "candidate")
}

func TestPersistInlineAgentEnvironmentMigratesLegacyTemplates(t *testing.T) {
	props, err := projectpkg.AgentDefinitionToServiceProperties(
		agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "basic-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		nil,
	)
	require.NoError(t, err)
	legacyEnvironment, err := structpb.NewValue([]any{
		map[string]any{
			"name":  "LEGACY_KEY",
			"value": "${LEGACY_KEY}",
		},
	})
	require.NoError(t, err)
	props.Fields["environmentVariables"] = legacyEnvironment
	svc := &azdext.ServiceConfig{
		Name:   "basic-agent",
		Host:   AiAgentHost,
		Config: props,
	}

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)
	require.NoError(t, persistInlineAgentEnvironment(
		t.Context(),
		client,
		svc,
		map[string]string{"OPTIMIZATION_CANDIDATE_ID": "candidate-1"},
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.added)
	require.Equal(
		t,
		[]string{"config.environmentVariables"},
		server.unsetPaths,
	)
	require.Equal(t, map[string]any{
		"LEGACY_KEY":                "${LEGACY_KEY}",
		"OPTIMIZATION_CANDIDATE_ID": "candidate-1",
	}, server.env["basic-agent"])
}

// TestPersistInlineAgentEnvironmentPreservesTopLevelEnv verifies a
// modern agent's top-level env templates survive the OPTIMIZATION_*
// update: they are read raw and rewritten via the env section, not
// snapshotted to expanded literals through AddService.
func TestPersistInlineAgentEnvironmentPreservesTopLevelEnv(t *testing.T) {
	props, err := projectpkg.AgentDefinitionToServiceProperties(
		agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "basic-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		nil,
	)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
		// Core forwards expanded values; the raw templates live on disk.
		Environment: map[string]string{
			"LOG_LEVEL":      "debug",
			"MODEL_ENDPOINT": "https://resolved.example",
		},
	}

	server := &recordingProjectServer{
		rawEnv: map[string]map[string]any{
			"basic-agent": {
				"LOG_LEVEL":      "${AZURE_LOG_LEVEL}",
				"MODEL_ENDPOINT": "$${{project.endpoint}}",
			},
		},
	}
	client := newProjectRecorderClient(t, server)
	require.NoError(t, persistInlineAgentEnvironment(
		t.Context(),
		client,
		svc,
		map[string]string{"OPTIMIZATION_CANDIDATE_ID": "candidate-1"},
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.added)
	require.Equal(
		t,
		[]string{"environmentVariables"},
		server.unsetPaths,
	)
	require.Equal(t, map[string]any{
		"LOG_LEVEL":                 "${AZURE_LOG_LEVEL}",
		"MODEL_ENDPOINT":            "$${{project.endpoint}}",
		"OPTIMIZATION_CANDIDATE_ID": "candidate-1",
	}, server.env["basic-agent"])
}

// TestPersistInlineAgentEnvironmentEscapesLegacyFoundrySpan verifies
// a legacy environmentVariables value carrying a raw Foundry ${{...}}
// span is escaped to $${{...}} when migrated into the env section.
func TestPersistInlineAgentEnvironmentEscapesLegacyFoundrySpan(t *testing.T) {
	props, err := projectpkg.AgentDefinitionToServiceProperties(
		agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "basic-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		nil,
	)
	require.NoError(t, err)
	legacyEnvironment, err := structpb.NewValue([]any{
		map[string]any{
			"name":  "SEARCH_KEY",
			"value": "${{connections.search.credentials.key}}",
		},
	})
	require.NoError(t, err)
	props.Fields["environmentVariables"] = legacyEnvironment
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)
	require.NoError(t, persistInlineAgentEnvironment(
		t.Context(),
		client,
		svc,
		map[string]string{"OPTIMIZATION_CANDIDATE_ID": "candidate-1"},
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(
		t,
		[]string{"environmentVariables"},
		server.unsetPaths,
	)
	require.Equal(t, map[string]any{
		"SEARCH_KEY":                "$${{connections.search.credentials.key}}",
		"OPTIMIZATION_CANDIDATE_ID": "candidate-1",
	}, server.env["basic-agent"])
}

func TestPersistInlineAgentEnvironmentNormalizesScalars(t *testing.T) {
	props, err := projectpkg.AgentDefinitionToServiceProperties(
		agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "basic-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		nil,
	)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
		Environment: map[string]string{
			"LARGE": "9007199254740993",
		},
	}
	server := &recordingProjectServer{
		rawEnv: map[string]map[string]any{
			"basic-agent": {
				"ENABLED": true,
				"RETRIES": float64(3),
				"EMPTY":   nil,
				"LARGE":   float64(9007199254740992),
			},
		},
	}

	client := newProjectRecorderClient(t, server)
	require.NoError(t, persistInlineAgentEnvironment(
		t.Context(),
		client,
		svc,
		map[string]string{"OPTIMIZATION_CANDIDATE_ID": "candidate-1"},
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(t, map[string]any{
		"ENABLED":                   "true",
		"RETRIES":                   "3",
		"EMPTY":                     "",
		"LARGE":                     "9007199254740993",
		"OPTIMIZATION_CANDIDATE_ID": "candidate-1",
	}, server.env["basic-agent"])
}

func TestPersistInlineAgentEnvironmentKeepsLegacyOnEnvFailure(
	t *testing.T,
) {
	props, err := projectpkg.AgentDefinitionToServiceProperties(
		agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "basic-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		nil,
	)
	require.NoError(t, err)
	legacyEnvironment, err := structpb.NewValue([]any{
		map[string]any{
			"name":  "LEGACY_KEY",
			"value": "${LEGACY_KEY}",
		},
	})
	require.NoError(t, err)
	props.Fields["environmentVariables"] = legacyEnvironment
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}
	server := &recordingProjectServer{
		setEnvironmentErr: fmt.Errorf("write failed"),
	}

	client := newProjectRecorderClient(t, server)
	err = persistInlineAgentEnvironment(
		t.Context(),
		client,
		svc,
		map[string]string{"OPTIMIZATION_CANDIDATE_ID": "candidate-1"},
	)

	require.ErrorContains(t, err, "write failed")
	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.unsetPaths)
	require.Empty(t, server.added)
}

// ---- printPreviewLines ----

func TestPrintPreviewLines(t *testing.T) {
	t.Parallel()

	// Disable color output so assertions don't need ANSI codes.
	color.NoColor = true

	tests := []struct {
		name   string
		lines  []string
		prefix string
		want   []string // substrings expected in output
	}{
		{
			"fewer lines than limit",
			[]string{"line1", "line2"},
			"+ ",
			[]string{"+ line1", "+ line2"},
		},
		{
			"exactly at limit",
			[]string{"a", "b", "c", "d"},
			"- ",
			[]string{"- a", "- b", "- c", "- d"},
		},
		{
			"exceeds limit shows truncation",
			[]string{"a", "b", "c", "d", "e", "f"},
			"+ ",
			[]string{"+ a", "+ b", "+ c", "+ d", "... (2 more lines)"},
		},
		{
			"empty lines",
			[]string{},
			"- ",
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			c := color.New(color.FgWhite)
			printPreviewLines(&buf, tt.lines, tt.prefix, c)
			out := buf.String()
			for _, s := range tt.want {
				assert.Contains(t, out, s)
			}
			if tt.want == nil {
				assert.Empty(t, out)
			}
		})
	}
}

// ---- printPromptDiff ----

func TestPrintPromptDiff(t *testing.T) {
	t.Parallel()

	color.NoColor = true

	t.Run("shows diff when baseline and candidate have instructions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		// Set up baseline with metadata that points to an instruction file.
		baselineDir := filepath.Join(dir, agentConfigsDir, opt_eval.BaselineDir)
		require.NoError(t, os.MkdirAll(baselineDir, 0750))
		require.NoError(t, os.WriteFile(
			filepath.Join(baselineDir, opt_eval.InstructionFile),
			[]byte("You are a baseline assistant.\nLine two."),
			0600,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(baselineDir, opt_eval.MetadataFile),
			[]byte("instruction_file: instructions.md\nmodel: gpt-4o\n"),
			0600,
		))

		candidateConfig := mustMarshal(t, map[string]any{
			"systemPrompt": "You are an optimized assistant.\nNew line two.\nNew line three.",
		})

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		out := buf.String()

		assert.Contains(t, out, "Instruction diff")
		assert.Contains(t, out, "Baseline")
		assert.Contains(t, out, "Optimized")
		assert.Contains(t, out, "You are a baseline assistant.")
		assert.Contains(t, out, "You are an optimized assistant.")
	})

	t.Run("no output when candidate has no instructions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		candidateConfig := mustMarshal(t, map[string]any{"model": "gpt-4o"})

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		assert.Empty(t, buf.String())
	})

	t.Run("no output when baseline config missing", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		candidateConfig := mustMarshal(t, map[string]any{"systemPrompt": "optimized"})

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		assert.Empty(t, buf.String())
	})

	t.Run("no output when baseline has no instruction file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		// Write metadata without instruction_file.
		baselineDir := filepath.Join(dir, agentConfigsDir, opt_eval.BaselineDir)
		require.NoError(t, os.MkdirAll(baselineDir, 0750))
		require.NoError(t, os.WriteFile(
			filepath.Join(baselineDir, opt_eval.MetadataFile),
			[]byte("model: gpt-4o\n"),
			0600,
		))

		candidateConfig := mustMarshal(t, map[string]any{"systemPrompt": "optimized"})

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		assert.Empty(t, buf.String())
	})
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

// ---- extractInstructions ----

func TestExtractInstructions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			"systemPrompt field",
			map[string]any{"systemPrompt": "You are a helpful assistant."},
			"You are a helpful assistant.",
		},
		{
			"system_prompt field (snake_case)",
			map[string]any{"system_prompt": "Snake-case prompt."},
			"Snake-case prompt.",
		},
		{
			"system_prompt takes precedence over camelCase",
			map[string]any{
				"system_prompt": "From snake_case",
				"systemPrompt":  "From camelCase",
			},
			"From snake_case",
		},
		{
			"instructions field",
			map[string]any{"instructions": "Follow the rules."},
			"Follow the rules.",
		},
		{
			"systemPrompt takes precedence",
			map[string]any{
				"systemPrompt": "From systemPrompt",
				"instructions": "From instructions",
			},
			"From systemPrompt",
		},
		{"nil config", nil, ""},
		{"empty map", map[string]any{}, ""},
		{"non-string value", map[string]any{"systemPrompt": 42}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, extractInstructions(tt.config))
		})
	}
}

// ---- agentConfigMetadata.resolveInstructions ----

func TestAgentConfigMetadata_ResolveInstructions(t *testing.T) {
	t.Parallel()
	t.Run("reads instruction file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "instructions.md"), []byte("Be helpful."), 0600))

		meta := &agentConfigMetadata{InstructionFile: "instructions.md"}
		assert.Equal(t, "Be helpful.", meta.resolveInstructions(dir))
	})

	t.Run("returns empty when no file set", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{}
		assert.Empty(t, meta.resolveInstructions(t.TempDir()))
	})

	t.Run("returns empty when file missing", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{InstructionFile: "nonexistent.md"}
		assert.Empty(t, meta.resolveInstructions(t.TempDir()))
	})
}

// ---- agentConfigMetadata.resolveSkillDir ----

func TestAgentConfigMetadata_ResolveSkillDir(t *testing.T) {
	t.Parallel()
	t.Run("returns empty when not set", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{}
		assert.Empty(t, meta.resolveSkillDir("/some/dir"))
	})

	t.Run("resolves relative path", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{SkillDir: "skills"}
		result := meta.resolveSkillDir("/project/config")
		assert.Equal(t, filepath.Join("/project/config", "skills"), result)
	})

	t.Run("preserves absolute path", func(t *testing.T) {
		t.Parallel()
		abs := filepath.Join(os.TempDir(), "absolute-skills")
		meta := &agentConfigMetadata{SkillDir: abs}
		assert.Equal(t, abs, meta.resolveSkillDir("/any/dir"))
	})
}

func TestAgentConfigMetadata_ResolveToolsFile(t *testing.T) {
	t.Parallel()
	t.Run("returns empty when not set", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{}
		assert.Empty(t, meta.resolveToolsFile("/some/dir"))
	})

	t.Run("resolves relative path", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{ToolsFile: "tools.json"}
		result := meta.resolveToolsFile("/project/config")
		assert.Equal(t, filepath.Join("/project/config", "tools.json"), result)
	})

	t.Run("preserves absolute path", func(t *testing.T) {
		t.Parallel()
		abs := filepath.Join(os.TempDir(), "absolute-tools.json")
		meta := &agentConfigMetadata{ToolsFile: abs}
		assert.Equal(t, abs, meta.resolveToolsFile("/any/dir"))
	})
}

// ---- writeAgentConfigFromCandidate ----

func TestWriteAgentConfigFromCandidate(t *testing.T) {
	t.Parallel()
	t.Run("writes metadata and instructions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		config := mustMarshal(t, map[string]any{
			"name":         "test-agent",
			"model":        "gpt-4o",
			"systemPrompt": "Test prompt.",
		})

		err := writeAgentConfigFromCandidate(dir, config)
		require.NoError(t, err)

		assert.FileExists(t, filepath.Join(dir, opt_eval.MetadataFile))
		assert.FileExists(t, filepath.Join(dir, opt_eval.InstructionFile))

		content, err := os.ReadFile(filepath.Join(dir, opt_eval.InstructionFile)) //nolint:gosec // test file path
		require.NoError(t, err)
		assert.Equal(t, "Test prompt.", string(content))
	})

	t.Run("writes inline skills", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		config := mustMarshal(t, map[string]any{
			"systemPrompt": "prompt",
			"skills": []any{
				map[string]any{
					"name":        "search",
					"description": "Search the web",
					"body":        "Search content here.",
				},
			},
		})

		err := writeAgentConfigFromCandidate(dir, config)
		require.NoError(t, err)

		skillFile := filepath.Join(dir, opt_eval.SkillsDir, "search", "SKILL.md")
		assert.FileExists(t, skillFile)
	})

	t.Run("handles nil config gracefully", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := writeAgentConfigFromCandidate(dir, json.RawMessage(`{}`))
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(dir, opt_eval.MetadataFile))
	})
}

// ---- isSkillFile ----

func TestIsSkillFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file optimize_api.CandidateFile
		want bool
	}{
		{"skill type", optimize_api.CandidateFile{Type: "skill", Path: "foo.md"}, true},
		{"skills path prefix", optimize_api.CandidateFile{Type: "file", Path: "skills/search/SKILL.md"}, true},
		{"other type and path", optimize_api.CandidateFile{Type: "file", Path: "config.yaml"}, false},
		{"empty", optimize_api.CandidateFile{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isSkillFile(tt.file))
		})
	}
}

// ---- isReservedEnvVarError ----

func TestIsReservedEnvVarError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"reserved for platform use", fmt.Errorf("variable is reserved for platform use"), true},
		{"AGENT_* variables", fmt.Errorf("AGENT_* variables are reserved"), true},
		{"unrelated error", fmt.Errorf("connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isReservedEnvVarError(tt.err))
		})
	}
}

// ---- writeToolsFile ----

func TestWriteToolsFile_NoTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := writeToolsFile(dir, map[string]any{"name": "agent"})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(dir, opt_eval.ToolsFile))
}

func TestWriteToolsFile_WithTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tools := []any{
		map[string]any{"type": "function", "function": map[string]any{"name": "search"}},
	}
	err := writeToolsFile(dir, map[string]any{"tools": tools})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, opt_eval.ToolsFile)) //nolint:gosec // test file path
	require.NoError(t, err)

	var parsed []any
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Len(t, parsed, 1)
}

func TestWriteAgentConfigFromCandidate_WithTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	config := mustMarshal(t, map[string]any{
		"systemPrompt": "prompt",
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "lookup_travel_policy"}},
		},
	})

	err := writeAgentConfigFromCandidate(dir, config)
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, opt_eval.ToolsFile))

	// Verify metadata references tools_file.
	metaData, err := os.ReadFile(filepath.Join(dir, opt_eval.MetadataFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	assert.Contains(t, string(metaData), "tools_file")
}

// ---- writeToolsFile: ignores legacy keys ----

func TestWriteToolsFile_IgnoresToolDefinitions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// toolDefinitions is no longer supported — should not produce a file.
	err := writeToolsFile(dir, map[string]any{
		"toolDefinitions": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "search"}},
		},
	})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(dir, opt_eval.ToolsFile))
}

func TestWriteToolsFile_IgnoresToolDescriptions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// toolDescriptions is no longer supported — should not produce a file.
	err := writeToolsFile(dir, map[string]any{
		"toolDescriptions": map[string]any{
			"lookup": map[string]any{"description": "Look up stuff"},
		},
	})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(dir, opt_eval.ToolsFile))
}

// ---- writeAgentConfigFromCandidate: model in metadata ----

func TestWriteAgentConfigFromCandidate_ModelInMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	config := mustMarshal(t, map[string]any{
		"name":         "travel-approver",
		"model":        "gpt-4o",
		"instructions": "Approve travel.",
	})

	err := writeAgentConfigFromCandidate(dir, config)
	require.NoError(t, err)

	metaData, err := os.ReadFile(filepath.Join(dir, opt_eval.MetadataFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	assert.Contains(t, string(metaData), "model: gpt-4o")
}

func TestWriteAgentConfigFromCandidate_NoModel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	config := mustMarshal(t, map[string]any{
		"name":         "travel-approver",
		"instructions": "Approve travel.",
	})

	err := writeAgentConfigFromCandidate(dir, config)
	require.NoError(t, err)

	metaData, err := os.ReadFile(filepath.Join(dir, opt_eval.MetadataFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	// model is omitempty, so it should not appear when not set.
	assert.NotContains(t, string(metaData), "model:")
}

// ---- writeAgentConfigFromCandidate: full candidate config ----

func TestWriteAgentConfigFromCandidate_FullConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	config := mustMarshal(t, map[string]any{
		"name":         "travel-approver",
		"model":        "gpt-4o",
		"instructions": "Review and approve travel requests.",
		"skills": []any{
			map[string]any{
				"name":        "policy-reviewer",
				"description": "Reviews travel requests",
				"body":        "# Policy Reviewer\nApprove everything.",
			},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "lookup_travel_policy",
					"description": "Look up travel policy",
				},
			},
		},
	})

	err := writeAgentConfigFromCandidate(dir, config)
	require.NoError(t, err)

	// Verify all files are created.
	assert.FileExists(t, filepath.Join(dir, opt_eval.MetadataFile))
	assert.FileExists(t, filepath.Join(dir, opt_eval.InstructionFile))
	assert.FileExists(t, filepath.Join(dir, opt_eval.ToolsFile))
	assert.FileExists(t, filepath.Join(dir, opt_eval.SkillsDir, "policy-reviewer", "SKILL.md"))

	// Verify metadata has all fields.
	metaData, err := os.ReadFile(filepath.Join(dir, opt_eval.MetadataFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	metaStr := string(metaData)
	assert.Contains(t, metaStr, "model: gpt-4o")
	assert.Contains(t, metaStr, "instruction_file")
	assert.Contains(t, metaStr, "skill_dir")
	assert.Contains(t, metaStr, "tools_file")

	// Verify instructions content.
	instrData, err := os.ReadFile(filepath.Join(dir, opt_eval.InstructionFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	assert.Equal(t, "Review and approve travel requests.", string(instrData))

	// Verify tools.json is a list.
	toolsData, err := os.ReadFile(filepath.Join(dir, opt_eval.ToolsFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	var toolsParsed []any
	require.NoError(t, json.Unmarshal(toolsData, &toolsParsed))
	assert.Len(t, toolsParsed, 1)
}
