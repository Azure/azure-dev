// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// writeOptimizeConfig lays out what `azd ai agent optimize` leaves behind:
// .agent_configs/baseline/metadata.yaml pointing at instructions.md beside it.
func writeOptimizeConfig(t *testing.T, serviceDir, metadata, instructions string) {
	t.Helper()
	dir := filepath.Join(serviceDir, ".agent_configs", "baseline")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "metadata.yaml"), []byte(metadata), 0o600))
	if instructions != "" {
		require.NoError(t,
			os.WriteFile(filepath.Join(dir, "instructions.md"), []byte(instructions), 0o600))
	}
}

// agentService builds a project holding one agent service, optionally
// declaring an agent name that differs from the service key.
func agentService(t *testing.T, root, serviceKey, declaredName string) *azdext.ProjectConfig {
	t.Helper()
	svc := &azdext.ServiceConfig{
		Name:         serviceKey,
		Host:         AgentHost,
		RelativePath: serviceKey,
	}
	if declaredName != "" {
		props, err := structpb.NewStruct(map[string]any{"name": declaredName})
		require.NoError(t, err)
		svc.AdditionalProperties = props
	}
	return &azdext.ProjectConfig{
		Path:     root,
		Services: map[string]*azdext.ServiceConfig{serviceKey: svc},
	}
}

// The instructions an agent was optimized with are already on disk, so
// generating from them needs no service call.
func TestAgentInstructionsFromProject_ReadsTheOptimizeConfig(t *testing.T) {
	root := t.TempDir()
	writeOptimizeConfig(t,
		filepath.Join(root, "support"),
		"name: support\ninstruction_file: instructions.md\n",
		"Answer support questions politely.\n")

	instruction, path, err := AgentInstructionsFromProject(
		agentService(t, root, "support", ""), "support")

	require.NoError(t, err)
	assert.Equal(t, "Answer support questions politely.", instruction)
	assert.Equal(t, filepath.Join(root, "support", ".agent_configs", "baseline", "instructions.md"),
		path)
}

// A target names the agent, which need not be spelled the way the azure.yaml
// key is. A user has only ever seen one of the two.
func TestAgentInstructionsFromProject_MatchesTheDeclaredAgentName(t *testing.T) {
	root := t.TempDir()
	writeOptimizeConfig(t,
		filepath.Join(root, "svc"),
		"instruction_file: instructions.md\n",
		"Be helpful.")

	instruction, _, err := AgentInstructionsFromProject(
		agentService(t, root, "svc", "support-agent"), "support-agent")

	require.NoError(t, err)
	assert.Equal(t, "Be helpful.", instruction)
}

// Most projects have never run optimize, so finding nothing is the ordinary
// case and has to leave the caller free to ask the service instead.
func TestAgentInstructionsFromProject_SilentWhenThereIsNothingToRead(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name  string
		proj  *azdext.ProjectConfig
		agent string
	}{
		{"no project at all", nil, "support"},
		{"no agent named", agentService(t, root, "support", ""), ""},
		{"no service by that name", agentService(t, root, "support", ""), "other"},
		{"no optimize config on disk", agentService(t, root, "support", ""), "support"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instruction, path, err := AgentInstructionsFromProject(tt.proj, tt.agent)

			assert.NoError(t, err)
			assert.Empty(t, instruction)
			assert.Empty(t, path)
		})
	}
}

// A service that is not an agent is not a candidate, however it is named.
func TestAgentInstructionsFromProject_IgnoresServicesThatAreNotAgents(t *testing.T) {
	root := t.TempDir()
	writeOptimizeConfig(t, filepath.Join(root, "support"),
		"instruction_file: instructions.md\n", "Be helpful.")

	proj := agentService(t, root, "support", "")
	proj.Services["support"].Host = "containerapp"

	instruction, _, err := AgentInstructionsFromProject(proj, "support")

	assert.NoError(t, err)
	assert.Empty(t, instruction)
}

// Two services answering to one name is a tie, and picking either would make
// the generated dataset describe an agent the caller did not mean.
func TestAgentInstructionsFromProject_RefusesAnAmbiguousTarget(t *testing.T) {
	root := t.TempDir()
	proj := agentService(t, root, "support", "")
	props, err := structpb.NewStruct(map[string]any{"name": "support"})
	require.NoError(t, err)
	proj.Services["helpdesk"] = &azdext.ServiceConfig{
		Name: "helpdesk", Host: AgentHost, RelativePath: "helpdesk",
		AdditionalProperties: props,
	}

	_, _, err = AgentInstructionsFromProject(proj, "support")

	require.ErrorIs(t, err, ErrAmbiguousAgentService)
	assert.Contains(t, err.Error(), "helpdesk")
	assert.Contains(t, err.Error(), "support")
	assert.Contains(t, err.Error(), "--target",
		"an ambiguity the caller can resolve has to say how")
}

// A pointer with nothing behind it means something wrote half the config.
// Falling back silently would generate from the published agent while the
// author believes they are generating from what they just optimized.
func TestAgentInstructionsFromProject_ReportsADanglingInstructionFile(t *testing.T) {
	root := t.TempDir()
	writeOptimizeConfig(t, filepath.Join(root, "support"),
		"instruction_file: instructions.md\n", "")

	_, _, err := AgentInstructionsFromProject(agentService(t, root, "support", ""), "support")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "instructions.md")
}

// Metadata that names no instruction file is a config without instructions,
// not a broken one.
func TestAgentInstructionsFromProject_NoInstructionFileIsNotAnError(t *testing.T) {
	root := t.TempDir()
	writeOptimizeConfig(t, filepath.Join(root, "support"), "name: support\n", "")

	instruction, _, err := AgentInstructionsFromProject(
		agentService(t, root, "support", ""), "support")

	assert.NoError(t, err)
	assert.Empty(t, instruction)
}

func TestAgentInstructionsFromProject_ReportsUnreadableMetadata(t *testing.T) {
	root := t.TempDir()
	writeOptimizeConfig(t, filepath.Join(root, "support"), "\tnot: [valid\n", "")

	_, _, err := AgentInstructionsFromProject(agentService(t, root, "support", ""), "support")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata.yaml")
}
