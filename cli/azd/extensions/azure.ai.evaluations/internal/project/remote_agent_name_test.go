// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// `init` writes the azure.yaml service key into an eval's `target.name`,
// because the key is what it had. The run API wants the name the agent is
// published under, and a service is free to declare one that differs -- so the
// key went out as the agent to invoke and the run graded the wrong agent, or
// nothing. These pin the translation between the two.
func TestRemoteAgentName(t *testing.T) {
	root := t.TempDir()

	t.Run("a service key becomes the name the service publishes", func(t *testing.T) {
		got, err := RemoteAgentName(agentService(t, root, "support", "hero-agent"), "support")

		require.NoError(t, err)
		assert.Equal(t, "hero-agent", got)
	})

	t.Run("the published name resolves to itself", func(t *testing.T) {
		// The same target is read again on every run, so translating it twice
		// has to be the same as translating it once.
		got, err := RemoteAgentName(agentService(t, root, "support", "hero-agent"), "hero-agent")

		require.NoError(t, err)
		assert.Equal(t, "hero-agent", got)
	})

	t.Run("a service declaring no name is already the agent name", func(t *testing.T) {
		got, err := RemoteAgentName(agentService(t, root, "support", ""), "support")

		require.NoError(t, err)
		assert.Equal(t, "support", got)
	})

	t.Run("an agent this project does not declare is left as written", func(t *testing.T) {
		// A configuration may name a remote agent that no local service builds.
		// The service is the one that gets to say whether it exists.
		got, err := RemoteAgentName(agentService(t, root, "support", ""), "somewhere-else")

		require.NoError(t, err)
		assert.Equal(t, "somewhere-else", got)
	})

	t.Run("outside a project there is nothing to resolve against", func(t *testing.T) {
		got, err := RemoteAgentName(nil, "support")

		require.NoError(t, err)
		assert.Equal(t, "support", got)
	})
}

// Two services answering to one name are two different agents, and a run bills
// against whichever is picked. Picking is what this refuses to do.
func TestRemoteAgentNameRefusesATie(t *testing.T) {
	shared, err := structpb.NewStruct(map[string]any{"name": "hero-agent"})
	require.NoError(t, err)

	proj := &azdext.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*azdext.ServiceConfig{
			// One service is keyed `hero-agent`; another publishes under it.
			"hero-agent": {Name: "hero-agent", Host: AgentHost, RelativePath: "a"},
			"support": {
				Name: "support", Host: AgentHost, RelativePath: "b",
				AdditionalProperties: shared,
			},
		},
	}

	_, err = RemoteAgentName(proj, "hero-agent")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "hero-agent")
	assert.Contains(t, err.Error(), "support", "the refusal has to name both candidates")
}
