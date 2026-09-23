// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimizeDeployCommand_HasRequiredFlags(t *testing.T) {
	cmd := newOptimizeDeployCommand(&azdext.ExtensionContext{})

	candidateFlag := cmd.Flags().Lookup("candidate")
	require.NotNil(t, candidateFlag, "--candidate flag should be registered")

	agentFlag := cmd.Flags().Lookup("agent")
	require.NotNil(t, agentFlag, "--agent flag should be registered")
}

func TestOptimizeDeployCommand_CandidateIsRequired(t *testing.T) {
	cmd := newOptimizeDeployCommand(&azdext.ExtensionContext{})

	// Set only --agent, omit --candidate
	cmd.SetArgs([]string{"--agent", "my-agent"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "candidate")
}

func TestOptimizeDeployCommand_AgentResolvedFromFlagOrProject(t *testing.T) {
	cmd := newOptimizeDeployCommand(&azdext.ExtensionContext{})

	// --agent is no longer MarkFlagRequired; it falls back to azd project
	agentFlag := cmd.Flags().Lookup("agent")
	require.NotNil(t, agentFlag)
	// Without --agent and without azd project context, should error about agent name
	cmd.SetArgs([]string{"--candidate", "cand_123"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent")
}

func TestOptimizeDeployCommand_HasConnectionFlags(t *testing.T) {
	cmd := newOptimizeDeployCommand(&azdext.ExtensionContext{})

	assert.NotNil(t, cmd.Flags().Lookup("endpoint"))
	assert.NotNil(t, cmd.Flags().Lookup("project-endpoint"))

	// Should NOT have subscription/resource-group/workspace
	assert.Nil(t, cmd.Flags().Lookup("subscription"))
	assert.Nil(t, cmd.Flags().Lookup("resource-group"))
	assert.Nil(t, cmd.Flags().Lookup("workspace"))
}

func TestOptimizeCommand_HasDeploySubCommand(t *testing.T) {
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	var actual []string
	for _, sub := range cmd.Commands() {
		actual = append(actual, sub.Name())
	}

	assert.Contains(t, actual, "deploy", "optimize should have 'deploy' sub-command")
}

func TestOptimizeDeployAgentHeaders(t *testing.T) {
	const projectEndpoint = "https://account.services.ai.azure.com/api/projects/test-project"
	const modelEndpoint = "https://account.services.ai.azure.com"

	tests := []struct {
		name       string
		definition map[string]any
		expected   map[string]string
	}{
		{
			name: "GitHub Copilot managed harness",
			definition: map[string]any{
				"kind":    "prompt",
				"harness": map[string]any{"type": agent_api.ManagedAgentHarnessGitHubCopilot},
			},
			expected: map[string]string{
				"Foundry-Features": agent_api.GitHubCopilotPreviewFeature,
				"x-model-endpoint": modelEndpoint,
			},
		},
		{
			name:       "plain prompt agent",
			definition: map[string]any{"kind": "prompt"},
			expected: map[string]string{
				"x-model-endpoint": modelEndpoint,
			},
		},
		{
			name: "plain prompt agent with skills",
			definition: map[string]any{
				"kind":   "prompt",
				"skills": []any{map[string]any{"name": "skill", "version": "1"}},
			},
			expected: map[string]string{
				"Foundry-Features": agent_api.SkillsPreviewFeature,
				"x-model-endpoint": modelEndpoint,
			},
		},
		{
			name: "managed prompt agent with skills",
			definition: map[string]any{
				"kind":    "prompt",
				"harness": map[string]any{"type": agent_api.ManagedAgentHarnessGitHubCopilot},
				"skills":  []any{map[string]any{"name": "skill", "version": "1"}},
			},
			expected: map[string]string{
				"Foundry-Features": agent_api.GitHubCopilotPreviewFeature + "," + agent_api.SkillsPreviewFeature,
				"x-model-endpoint": modelEndpoint,
			},
		},
		{
			name: "other harness",
			definition: map[string]any{
				"kind":    "prompt",
				"harness": map[string]any{"type": "future_harness"},
			},
			expected: map[string]string{
				"x-model-endpoint": modelEndpoint,
			},
		},
		{
			name: "non-prompt agent with managed harness field",
			definition: map[string]any{
				"kind":    "hosted",
				"harness": map[string]any{"type": agent_api.ManagedAgentHarnessGitHubCopilot},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, optimizeDeployAgentHeaders(tt.definition, projectEndpoint))
		})
	}
}

func TestOptimizationPromotionHeaders(t *testing.T) {
	require.Nil(t, optimizationPromotionHeaders(false))
	require.Equal(t, map[string]string{
		optimize_api.PromotionReportOnlyHeader: "true",
	}, optimizationPromotionHeaders(true))
}

func TestBuildPromptDeployDefinition(t *testing.T) {
	current := map[string]any{
		"kind":         "prompt",
		"model":        "old-model",
		"instructions": "old instructions",
		"harness":      map[string]any{"type": agent_api.ManagedAgentHarnessGitHubCopilot},
		"skills":       []any{map[string]any{"name": "existing-skill", "version": "1"}},
		"tools": []any{
			map[string]any{
				"type":        "function",
				"name":        "lookup",
				"description": "old description",
				"parameters": map[string]any{
					"type": "object",
				},
			},
			map[string]any{"type": "code_interpreter"},
		},
	}
	candidate := json.RawMessage(`{
		"model": "new-model",
		"system_prompt": "new instructions",
		"tools": [{
			"type": "function",
			"name": "lookup",
			"description": "new description",
			"parameters": {
				"properties": {
					"query": {"type": "string"}
				}
			}
		}]
	}`)

	got, err := buildPromptDeployDefinition(current, candidate)
	require.NoError(t, err)
	require.Equal(t, "new-model", got["model"])
	require.Equal(t, "new instructions", got["instructions"])
	require.Equal(t, current["harness"], got["harness"])
	require.Equal(t, current["skills"], got["skills"])

	tools, ok := got["tools"].([]any)
	require.True(t, ok)
	functionTool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "new description", functionTool["description"])
	parameters, ok := functionTool["parameters"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "object", parameters["type"])
	require.Contains(t, parameters, "properties")
	require.Equal(t, map[string]any{"type": "code_interpreter"}, tools[1])
}

func TestExtractEnvVars_EmptyDef(t *testing.T) {
	def := map[string]any{"kind": "hosted"}
	result := extractEnvVars(def)
	assert.Empty(t, result)
}

func TestExtractEnvVars_WithVars(t *testing.T) {
	def := map[string]any{
		"kind": "hosted",
		"environment_variables": map[string]any{
			"FOO": "bar",
			"BAZ": "qux",
		},
	}
	result := extractEnvVars(def)
	assert.Equal(t, "bar", result["FOO"])
	assert.Equal(t, "qux", result["BAZ"])
	assert.Len(t, result, 2)
}

func TestBuildDeployDefinition_PreservesFieldsAndOverridesEnvVars(t *testing.T) {
	currentDef := map[string]any{
		"kind":                    "hosted",
		"container_configuration": map[string]any{"image": "myimage:latest"},
		"cpu":                     "1.0",
		"memory":                  "2Gi",
		"environment_variables": map[string]any{
			"EXISTING_VAR": "keep_me",
		},
	}

	envVars := map[string]string{
		"EXISTING_VAR":        "keep_me",
		"OPTIMIZATION_CONFIG": `{"key":"value"}`,
	}

	newDef := buildDeployDefinition(currentDef, envVars)

	assert.Equal(t, "hosted", newDef["kind"])
	containerConfig := newDef["container_configuration"].(map[string]any)
	assert.Equal(t, "myimage:latest", containerConfig["image"])
	assert.Equal(t, "1.0", newDef["cpu"])
	assert.Equal(t, "2Gi", newDef["memory"])

	newEnvVars, ok := newDef["environment_variables"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "keep_me", newEnvVars["EXISTING_VAR"])
	assert.Equal(t, `{"key":"value"}`, newEnvVars["OPTIMIZATION_CONFIG"])
}

func TestBuildDeployDefinition_NormalizesProtocolVersion(t *testing.T) {
	currentDef := map[string]any{
		"kind":                    "hosted",
		"container_configuration": map[string]any{"image": "myimage:latest"},
		"cpu":                     "1.0",
		"memory":                  "2Gi",
		"container_protocol_versions": []any{
			map[string]any{"protocol": "responses", "version": "v1"},
		},
		"environment_variables": map[string]any{},
	}

	newDef := buildDeployDefinition(currentDef, map[string]string{"FOO": "bar"})

	// Legacy field should be migrated to protocol_versions
	protocols := newDef["protocol_versions"].([]any)
	p := protocols[0].(map[string]any)
	assert.Equal(t, "1.0.0", p["version"], "v1 should be normalized to 1.0.0")
	assert.Equal(t, "responses", p["protocol"])
	// Legacy field should be removed
	assert.Nil(t, newDef["container_protocol_versions"])
}

func TestNormalizeProtocolVersions_NoOp(t *testing.T) {
	// Already using new field name with 1.0.0 — should not change
	def := map[string]any{
		"protocol_versions": []any{
			map[string]any{"protocol": "responses", "version": "1.0.0"},
		},
	}
	normalizeProtocolVersions(def)

	protocols := def["protocol_versions"].([]any)
	p := protocols[0].(map[string]any)
	assert.Equal(t, "1.0.0", p["version"])
}

func TestNormalizeProtocolVersions_MissingField(t *testing.T) {
	def := map[string]any{"kind": "hosted"}
	normalizeProtocolVersions(def) // should not panic
}

func TestNormalizeContainerImage_MigratesLegacy(t *testing.T) {
	def := map[string]any{
		"kind":  "hosted",
		"image": "myimage:latest",
	}
	normalizeContainerImage(def)

	containerConfig, ok := def["container_configuration"].(map[string]any)
	require.True(t, ok, "container_configuration should be set")
	assert.Equal(t, "myimage:latest", containerConfig["image"])
	assert.Nil(t, def["image"], "legacy image field should be removed")
}

func TestNormalizeContainerImage_NoOpWhenNewSchema(t *testing.T) {
	def := map[string]any{
		"kind":                    "hosted",
		"container_configuration": map[string]any{"image": "existing:v1"},
	}
	normalizeContainerImage(def)

	containerConfig := def["container_configuration"].(map[string]any)
	assert.Equal(t, "existing:v1", containerConfig["image"])
}

func TestNormalizeContainerImage_NoOpWhenNoImage(t *testing.T) {
	def := map[string]any{"kind": "hosted"}
	normalizeContainerImage(def) // should not panic
	assert.Nil(t, def["container_configuration"])
}

// ---- upsertAgentYamlEnvVar ----

func TestUpsertAgentYamlEnvVar_InsertsNew(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "agent.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("name: test-agent\n"), 0600))

	err := upsertAgentYamlEnvVar(yamlPath, "MY_VAR", "my_value")
	require.NoError(t, err)

	data, err := os.ReadFile(yamlPath) //nolint:gosec // test file path
	require.NoError(t, err)
	assert.Contains(t, string(data), "MY_VAR")
	assert.Contains(t, string(data), "my_value")
}

func TestUpsertAgentYamlEnvVar_UpdatesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "agent.yaml")
	content := `name: test-agent
environment_variables:
  - name: MY_VAR
    value: old_value
`
	require.NoError(t, os.WriteFile(yamlPath, []byte(content), 0600))

	err := upsertAgentYamlEnvVar(yamlPath, "MY_VAR", "new_value")
	require.NoError(t, err)

	data, err := os.ReadFile(yamlPath) //nolint:gosec // test file path
	require.NoError(t, err)
	assert.Contains(t, string(data), "new_value")
	assert.NotContains(t, string(data), "old_value")
}

func TestUpsertAgentYamlEnvVar_FileMissing(t *testing.T) {
	t.Parallel()
	err := upsertAgentYamlEnvVar("/nonexistent/agent.yaml", "KEY", "VALUE")
	assert.Error(t, err)
}
