// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingDependencyRunner struct {
	args   []string
	output []byte
	err    error
}

func (r *recordingDependencyRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.args = append([]string(nil), args...)
	return r.output, r.err
}

func TestAgentAddToolboxReference(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`# agent definition
name: research-agent
kind: hosted
language: python
`), 0o600))

	require.NoError(t, addAgentToolboxReference(path, toolboxReference("support-tools", "2")))
	reference, err := loadAgentToolboxReference(path)
	require.NoError(t, err)
	require.NotNil(t, reference)
	assert.Equal(t, "support-tools", reference.Name)
	assert.Equal(t, "2", reference.Version)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(content), "# agent definition")
}

func TestAgentAddToolboxReferenceRejectsDifferentExistingReference(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: research-agent
kind: hosted
toolbox:
  name: existing
`), 0o600))

	err := addAgentToolboxReference(path, toolboxReference("replacement", ""))
	require.Error(t, err)
}

func TestDeployAgentToolboxDependencyUsesRemoteReference(t *testing.T) {
	t.Parallel()

	agentPath := filepath.Join(t.TempDir(), "agent.yaml")
	runner := &recordingDependencyRunner{}
	environment, err := deployAgentToolboxDependency(
		t.Context(), runner,
		"https://account.services.ai.azure.com/api/projects/project",
		agentPath,
		pointerToolboxReference("support-tools", "3"),
	)
	require.NoError(t, err)
	assert.Empty(t, runner.args)
	assert.Equal(t, "support-tools", environment["TOOLBOX_NAME"])
	assert.Equal(t, "3", environment["TOOLBOX_VERSION"])
	assert.Equal(
		t,
		"https://account.services.ai.azure.com/api/projects/project/toolboxes/support-tools/versions/3/mcp?api-version=v1",
		environment["TOOLBOX_ENDPOINT"],
	)
}

func TestDeployAgentToolboxDependencyEscapesPinnedReference(t *testing.T) {
	t.Parallel()

	agentPath := filepath.Join(t.TempDir(), "agent.yaml")
	runner := &recordingDependencyRunner{}
	environment, err := deployAgentToolboxDependency(
		t.Context(), runner,
		"https://account.services.ai.azure.com/api/projects/project",
		agentPath,
		pointerToolboxReference("support/tools?#", "3/preview?#"),
	)
	require.NoError(t, err)
	assert.Empty(t, runner.args)
	assert.Equal(
		t,
		"https://account.services.ai.azure.com/api/projects/project/"+
			"toolboxes/support%2Ftools%3F%23/versions/3%2Fpreview%3F%23/mcp?api-version=v1",
		environment["TOOLBOX_ENDPOINT"],
	)
}

func TestDeployAgentToolboxDependencyPinnedVersionSkipsSiblingDefinition(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	agentPath := filepath.Join(directory, "agent.yaml")
	require.NoError(t, os.WriteFile(
		filepath.Join(directory, "toolbox.yaml"),
		[]byte("name: support-tools\n"),
		0o600,
	))
	runner := &recordingDependencyRunner{}
	environment, err := deployAgentToolboxDependency(
		t.Context(), runner, "https://example", agentPath,
		pointerToolboxReference("support-tools", "3"),
	)
	require.NoError(t, err)
	assert.Empty(t, runner.args)
	assert.Equal(t, "3", environment["TOOLBOX_VERSION"])
}

func TestDeployAgentToolboxDependencyDeploysSiblingDefinition(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	agentPath := filepath.Join(directory, "agent.yaml")
	toolboxPath := filepath.Join(directory, "toolbox.yaml")
	require.NoError(t, os.WriteFile(toolboxPath, []byte("name: support-tools\n"), 0o600))
	runner := &recordingDependencyRunner{output: []byte(`{
  "toolbox": "support-tools",
  "version": "4",
  "endpoint": "https://example/toolboxes/support-tools/versions/4/mcp?api-version=v1"
}`)}

	environment, err := deployAgentToolboxDependency(
		t.Context(), runner, "https://example", agentPath,
		pointerToolboxReference("support-tools", ""),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ai", "toolbox", "deploy", toolboxPath,
		"--project-endpoint", "https://example",
		"--output", "json", "--no-prompt",
	}, runner.args)
	assert.Equal(t, "4", environment["TOOLBOX_VERSION"])
	assert.Equal(t, runnerOutputEndpoint(), environment["TOOLBOX_ENDPOINT"])
}

func TestRunAgentDeployPassesToolboxEnvironment(t *testing.T) {
	directory := t.TempDir()
	agentPath := filepath.Join(directory, "agent.yaml")
	toolboxPath := filepath.Join(directory, "toolbox.yaml")
	require.NoError(t, os.WriteFile(agentPath, []byte(`
name: research-agent
kind: hosted
toolbox:
  name: support-tools
`), 0o600))
	require.NoError(t, os.WriteFile(toolboxPath, []byte("name: support-tools\n"), 0o600))
	runner := &recordingDependencyRunner{output: []byte(`{
  "toolbox": "support-tools",
  "version": "4",
  "endpoint": "https://example/toolboxes/support-tools/versions/4/mcp?api-version=v1"
}`)}

	var got project.DirectDeployOptions
	deployer := func(_ context.Context, options project.DirectDeployOptions) (*project.DirectDeployResult, error) {
		got = options
		return &project.DirectDeployResult{Name: "research-agent", Version: "1", State: "active"}, nil
	}
	err := runAgentDeploy(
		t.Context(), agentPath,
		agentDeployFlags{projectEndpoint: "https://account.services.ai.azure.com/api/projects/project"},
		"json", runner, deployer,
	)
	require.NoError(t, err)
	assert.Equal(t, agentPath, got.DefinitionPath)
	assert.Equal(t, "support-tools", got.Environment["TOOLBOX_NAME"])
	assert.Equal(t, runnerOutputEndpoint(), got.Environment["TOOLBOX_ENDPOINT"])
}

func toolboxReference(name, version string) agent_yaml.ToolboxReference {
	return agent_yaml.ToolboxReference{Name: name, Version: version}
}

func pointerToolboxReference(name, version string) *agent_yaml.ToolboxReference {
	reference := toolboxReference(name, version)
	return &reference
}

func runnerOutputEndpoint() string {
	return "https://example/toolboxes/support-tools/versions/4/mcp?api-version=v1"
}
