// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
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

	var gotOptions project.DirectDeployOptions
	preparer := func(
		_ context.Context,
		options project.DirectDeployOptions,
	) (*project.PreparedStandaloneHostedAgent, error) {
		gotOptions = options
		return &project.PreparedStandaloneHostedAgent{}, nil
	}
	var gotEnvironment map[string]string
	deployer := func(
		_ context.Context,
		_ *project.PreparedStandaloneHostedAgent,
		environment map[string]string,
	) (*project.DirectDeployResult, error) {
		gotEnvironment = environment
		return &project.DirectDeployResult{Name: "research-agent", Version: "1", State: "active"}, nil
	}
	err := runAgentDeploy(
		t.Context(), agentPath,
		agentDeployFlags{projectEndpoint: "https://account.services.ai.azure.com/api/projects/project"},
		"json", runner, preparer, deployer,
	)
	require.NoError(t, err)
	assert.Equal(t, agentPath, gotOptions.DefinitionPath)
	assert.Equal(t, "support-tools", gotEnvironment["TOOLBOX_NAME"])
	assert.Equal(t, runnerOutputEndpoint(), gotEnvironment["TOOLBOX_ENDPOINT"])
}

func TestRunAgentDeployPreparesBeforeToolboxDeployment(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	agentPath := filepath.Join(directory, "agent.yaml")
	require.NoError(t, os.WriteFile(agentPath, []byte(`
name: research-agent
kind: hosted
toolbox:
  name: support-tools
`), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(directory, "toolbox.yaml"),
		[]byte("name: support-tools\n"),
		0o600,
	))
	runner := &recordingDependencyRunner{}
	prepareErr := errors.New("agent preparation failed")
	preparer := func(
		context.Context,
		project.DirectDeployOptions,
	) (*project.PreparedStandaloneHostedAgent, error) {
		return nil, prepareErr
	}
	deployer := func(
		context.Context,
		*project.PreparedStandaloneHostedAgent,
		map[string]string,
	) (*project.DirectDeployResult, error) {
		t.Fatal("deployer must not run after preparation fails")
		return nil, nil
	}

	err := runAgentDeploy(
		t.Context(), agentPath,
		agentDeployFlags{projectEndpoint: "https://account.services.ai.azure.com/api/projects/project"},
		"json", runner, preparer, deployer,
	)
	require.ErrorIs(t, err, prepareErr)
	assert.Empty(t, runner.args, "Toolbox deployment must not run before Agent preparation succeeds")
}

func TestRunAgentDeployMissingSourceDoesNotDeployToolbox(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	agentPath := filepath.Join(directory, "agent.yaml")
	require.NoError(t, os.WriteFile(agentPath, []byte(`
name: research-agent
kind: hosted
language: python
toolbox:
  name: support-tools
`), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(directory, "toolbox.yaml"),
		[]byte("name: support-tools\n"),
		0o600,
	))
	runner := &recordingDependencyRunner{}
	deployer := func(
		context.Context,
		*project.PreparedStandaloneHostedAgent,
		map[string]string,
	) (*project.DirectDeployResult, error) {
		t.Fatal("deployer must not run when the Agent source directory is missing")
		return nil, nil
	}

	err := runAgentDeploy(
		t.Context(), agentPath,
		agentDeployFlags{
			projectEndpoint: "https://account.services.ai.azure.com/api/projects/project",
			codePath:        filepath.Join(directory, "missing"),
		},
		"json", runner, project.PrepareStandaloneHostedAgent, deployer,
	)
	require.Error(t, err)
	assert.Empty(t, runner.args, "Toolbox deployment must not run before Agent source validation succeeds")
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
