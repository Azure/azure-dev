// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimizeDeployCommand_HasRequiredFlags(t *testing.T) {
	cmd := newOptimizeDeployCommand()

	candidateFlag := cmd.Flags().Lookup("candidate")
	require.NotNil(t, candidateFlag, "--candidate flag should be registered")

	agentFlag := cmd.Flags().Lookup("agent")
	require.NotNil(t, agentFlag, "--agent flag should be registered")
}

func TestOptimizeDeployCommand_CandidateIsRequired(t *testing.T) {
	cmd := newOptimizeDeployCommand()

	// Set only --agent, omit --candidate
	cmd.SetArgs([]string{"--agent", "my-agent"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "candidate")
}

func TestOptimizeDeployCommand_AgentResolvedFromFlagOrYaml(t *testing.T) {
	cmd := newOptimizeDeployCommand()

	// --agent is no longer MarkFlagRequired; it falls back to agent.yaml
	agentFlag := cmd.Flags().Lookup("agent")
	require.NotNil(t, agentFlag)
	// Without --agent and without agent.yaml, should error about agent name
	cmd.SetArgs([]string{"--candidate", "cand_123"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent")
}

func TestOptimizeDeployCommand_HasConnectionFlags(t *testing.T) {
	cmd := newOptimizeDeployCommand()

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
		"kind":   "hosted",
		"image":  "myimage:latest",
		"cpu":    "1.0",
		"memory": "2Gi",
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
	assert.Equal(t, "myimage:latest", newDef["image"])
	assert.Equal(t, "1.0", newDef["cpu"])
	assert.Equal(t, "2Gi", newDef["memory"])

	newEnvVars, ok := newDef["environment_variables"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "keep_me", newEnvVars["EXISTING_VAR"])
	assert.Equal(t, `{"key":"value"}`, newEnvVars["OPTIMIZATION_CONFIG"])
}

func TestBuildDeployDefinition_NormalizesProtocolVersion(t *testing.T) {
	currentDef := map[string]any{
		"kind":   "hosted",
		"image":  "myimage:latest",
		"cpu":    "1.0",
		"memory": "2Gi",
		"container_protocol_versions": []any{
			map[string]any{"protocol": "responses", "version": "v1"},
		},
		"environment_variables": map[string]any{},
	}

	newDef := buildDeployDefinition(currentDef, map[string]string{"FOO": "bar"})

	protocols := newDef["container_protocol_versions"].([]any)
	p := protocols[0].(map[string]any)
	assert.Equal(t, "1.0.0", p["version"], "v1 should be normalized to 1.0.0")
	assert.Equal(t, "responses", p["protocol"])
}

func TestNormalizeProtocolVersions_NoOp(t *testing.T) {
	// Already 1.0.0 — should not change
	def := map[string]any{
		"container_protocol_versions": []any{
			map[string]any{"protocol": "responses", "version": "1.0.0"},
		},
	}
	normalizeProtocolVersions(def)

	protocols := def["container_protocol_versions"].([]any)
	p := protocols[0].(map[string]any)
	assert.Equal(t, "1.0.0", p["version"])
}

func TestNormalizeProtocolVersions_MissingField(t *testing.T) {
	def := map[string]any{"kind": "hosted"}
	normalizeProtocolVersions(def) // should not panic
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
