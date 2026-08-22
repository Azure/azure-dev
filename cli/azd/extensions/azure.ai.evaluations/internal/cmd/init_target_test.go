// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectWithHosts builds a project whose services carry hosts, which is what
// agent detection keys on. The sibling projectWith helper sets names only.
func projectWithHosts(services map[string]string) *azdext.ProjectConfig {
	svcs := map[string]*azdext.ServiceConfig{}
	for name, host := range services {
		svcs[name] = &azdext.ServiceConfig{Name: name, Host: host}
	}
	return &azdext.ProjectConfig{Services: svcs}
}

// The spec's first hero command is `azd ai eval init --source traces`, with no
// target. Requiring the flag put it in front of the one command a developer
// runs first.
func TestAgentServices_FindsTheOnlyAgent(t *testing.T) {
	proj := projectWithHosts(map[string]string{
		"ai-project":    "azure.ai.project",
		"support-agent": project.AgentHost,
	})

	agents := agentServices(proj)

	require.Len(t, agents, 1)
	assert.Equal(t, "support-agent", agents[0])
}

// Sorted, so a prompt and an error list them the same way twice.
func TestAgentServices_AreSorted(t *testing.T) {
	proj := projectWithHosts(map[string]string{
		"zebra-agent":   project.AgentHost,
		"alpha-agent":   project.AgentHost,
		"ai-project":    "azure.ai.project",
		"support-agent": project.AgentHost,
	})

	assert.Equal(t, []string{"alpha-agent", "support-agent", "zebra-agent"}, agentServices(proj))
}

func TestAgentServices_NoneWhenTheProjectHasNoAgent(t *testing.T) {
	proj := projectWithHosts(map[string]string{"ai-project": "azure.ai.project"})

	assert.Empty(t, agentServices(proj))
}

// A project with no agent cannot be scaffolded, and the error has to say that
// rather than name a flag the developer has nothing to put in.
func TestResolveAgentTarget_NoAgent(t *testing.T) {
	cmd := newInitCommand()

	_, err := resolveAgentTarget(cmd, projectWithHosts(map[string]string{
		"ai-project": "azure.ai.project",
	}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no agent service")
}

// With several agents there is nothing to detect. Under --no-prompt the flag is
// the only way to say which, so the error names it and lists the candidates.
func TestResolveAgentTarget_AmbiguousUnderNoPrompt(t *testing.T) {
	cmd := newInitCommand()
	// --no-prompt is inherited from the root in the real tree, so the test has
	// to supply it the way the root does.
	cmd.Flags().Bool("no-prompt", true, "")

	_, err := resolveAgentTarget(cmd, projectWithHosts(map[string]string{
		"one-agent": project.AgentHost,
		"two-agent": project.AgentHost,
	}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--target")
	assert.Contains(t, err.Error(), "one-agent")
	assert.Contains(t, err.Error(), "two-agent")
}

func TestResolveAgentTarget_DetectsTheSoleAgent(t *testing.T) {
	cmd := newInitCommand()

	target, err := resolveAgentTarget(cmd, projectWithHosts(map[string]string{
		"ai-project":    "azure.ai.project",
		"support-agent": project.AgentHost,
	}))

	require.NoError(t, err)
	assert.Equal(t, "support-agent", target)
}
