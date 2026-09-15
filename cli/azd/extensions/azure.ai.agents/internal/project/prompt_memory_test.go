// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

	"github.com/stretchr/testify/require"
)

// fakeMemoryStoreEnsurer records the request it was handed so tests can assert
// what azd would send, without a live Foundry endpoint.
type fakeMemoryStoreEnsurer struct {
	request *azure.CreateMemoryStoreRequest
	store   *azure.MemoryStoreObject
	err     error
}

func (f *fakeMemoryStoreEnsurer) EnsureMemoryStore(
	_ context.Context, request *azure.CreateMemoryStoreRequest,
) (*azure.MemoryStoreObject, bool, error) {
	f.request = request
	if f.err != nil {
		return nil, false, f.err
	}
	if f.store != nil {
		return f.store, false, nil
	}
	return &azure.MemoryStoreObject{Name: request.Name, Id: "store-1"}, true, nil
}

func newMemoryTestGraph(memory *agent_yaml.PromptMemory) (*promptGraph, *fakeMemoryStoreEnsurer, *promptNode) {
	managed := &agent_yaml.PromptAgent{Memory: memory}
	managed.Name = "agent-1"

	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeMemoryStoreEnsurer{}
	node := memoryNode(g, memory, func() (memoryStoreEnsurer, error) { return fake, nil })
	return g, fake, node
}

// TestMemoryNode_NotRegisteredWithoutMemory verifies no node (and therefore no
// store provisioning) happens for an agent that declares no memory.
func TestMemoryNode_NotRegisteredWithoutMemory(t *testing.T) {
	t.Parallel()

	_, _, node := newMemoryTestGraph(nil)
	require.Nil(t, node)
}

// TestMemoryNode_Validate covers the required fields. Validation runs before any
// node resolves, so catching these here means a misconfigured memory block never
// gets as far as provisioning a model deployment or a vector store.
func TestMemoryNode_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		memory  agent_yaml.PromptMemory
		wantErr string
	}{
		{
			name:    "missing store name",
			memory:  agent_yaml.PromptMemory{ChatModel: "c", EmbeddingModel: "e"},
			wantErr: "memory requires a store name",
		},
		{
			name:    "missing chat model",
			memory:  agent_yaml.PromptMemory{Store: "s", EmbeddingModel: "e"},
			wantErr: "memory.chat_model",
		},
		{
			name:    "missing embedding model",
			memory:  agent_yaml.PromptMemory{Store: "s", ChatModel: "c"},
			wantErr: "memory.embedding_model",
		},
		{
			name:   "complete",
			memory: agent_yaml.PromptMemory{Store: "s", ChatModel: "c", EmbeddingModel: "e"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, node := newMemoryTestGraph(&tc.memory)
			require.NotNil(t, node)

			err := node.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestMemoryNode_ResolveInjectsTool is the core assertion of the feature: the
// prompt-agent API has no memory field, so memory only works if resolving the
// node adds a memory_search_preview tool pointing at the provisioned store.
func TestMemoryNode_ResolveInjectsTool(t *testing.T) {
	t.Parallel()

	updateDelay := 300
	maxMemories := 5
	enabled := true

	memory := &agent_yaml.PromptMemory{
		Store:          "support-memory",
		Description:    "Support conversations",
		ChatModel:      "gpt-4.1-mini",
		EmbeddingModel: "text-embedding-3-small",
		Scope:          "user_123",
		UpdateDelay:    &updateDelay,
		MaxMemories:    &maxMemories,
		Options:        &agent_yaml.PromptMemoryOptions{UserProfileEnabled: &enabled},
	}

	g, fake, node := newMemoryTestGraph(memory)
	require.NoError(t, node.Resolve(t.Context()))

	// The store is created from the declared models.
	require.Equal(t, "support-memory", fake.request.Name)
	require.Equal(t, "Support conversations", fake.request.Description)
	require.Equal(t, azure.MemoryStoreKindDefault, fake.request.Definition.Kind)
	require.Equal(t, "gpt-4.1-mini", fake.request.Definition.ChatModel)
	require.Equal(t, "text-embedding-3-small", fake.request.Definition.EmbeddingModel)
	require.NotNil(t, fake.request.Definition.Options)
	require.True(t, *fake.request.Definition.Options.UserProfileEnabled)

	require.Equal(t, "support-memory", g.bindings[memoryStoreBindingKey])

	require.Len(t, g.managed.Tools, 1)
	tool, ok := g.managed.Tools[0].(map[string]any)
	require.True(t, ok, "tool: got %T", g.managed.Tools[0])

	require.Equal(t, "memory_search_preview", tool["type"])
	require.Equal(t, "support-memory", tool["memory_store_name"])
	require.Equal(t, "user_123", tool["scope"])
	require.Equal(t, 300, tool["update_delay"])
	require.Equal(t, map[string]any{"max_memories": 5}, tool["search_options"])
}

// TestMemoryNode_ResolveDefaultsScope verifies an unset scope falls back to the
// per-caller default. A shared default would let one user's memories surface in
// another user's conversation, so this is a privacy boundary, not a nicety.
func TestMemoryNode_ResolveDefaultsScope(t *testing.T) {
	t.Parallel()

	memory := &agent_yaml.PromptMemory{Store: "s", ChatModel: "c", EmbeddingModel: "e"}
	g, _, node := newMemoryTestGraph(memory)
	require.NoError(t, node.Resolve(t.Context()))

	tool, ok := g.managed.Tools[0].(map[string]any)
	require.True(t, ok, "tool: got %T", g.managed.Tools[0])
	require.Equal(t, agent_yaml.DefaultMemoryScope, tool["scope"])

	// Optional knobs stay absent so the service applies its own defaults rather
	// than azd pinning them to a zero value.
	require.NotContains(t, tool, "update_delay")
	require.NotContains(t, tool, "search_options")
}

// TestMemoryNode_ResolveReplacesExistingTool verifies re-deploying updates the
// existing tool in place. Appending instead would accumulate a duplicate
// memory_search_preview entry on every deploy.
func TestMemoryNode_ResolveReplacesExistingTool(t *testing.T) {
	t.Parallel()

	memory := &agent_yaml.PromptMemory{Store: "new-store", ChatModel: "c", EmbeddingModel: "e"}
	g, _, node := newMemoryTestGraph(memory)
	g.managed.Tools = []any{
		map[string]any{"type": "code_interpreter"},
		map[string]any{"type": "memory_search_preview", "memory_store_name": "old-store"},
	}

	require.NoError(t, node.Resolve(t.Context()))

	require.Len(t, g.managed.Tools, 2)
	tool, ok := g.managed.Tools[1].(map[string]any)
	require.True(t, ok, "tool: got %T", g.managed.Tools[1])
	require.Equal(t, "new-store", tool["memory_store_name"])
}

// TestMemoryNode_ResolveWrapsError verifies a provisioning failure names the
// store, so the user knows which resource to look at.
func TestMemoryNode_ResolveWrapsError(t *testing.T) {
	t.Parallel()

	memory := &agent_yaml.PromptMemory{Store: "support-memory", ChatModel: "c", EmbeddingModel: "e"}
	managed := &agent_yaml.PromptAgent{Memory: memory}
	managed.Name = "agent-1"

	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeMemoryStoreEnsurer{err: errors.New("boom")}
	node := memoryNode(g, memory, func() (memoryStoreEnsurer, error) { return fake, nil })

	err := node.Resolve(t.Context())
	require.ErrorContains(t, err, "support-memory")
	require.ErrorContains(t, err, "boom")
	require.Empty(t, g.managed.Tools, "no tool should be injected when the store fails")
}

// TestNewFoundryMemoryStoreEnsurer_RequiresEndpoint verifies the live ensurer
// refuses to build without a project endpoint rather than failing later with an
// opaque request error.
func TestNewFoundryMemoryStoreEnsurer_RequiresEndpoint(t *testing.T) {
	t.Parallel()

	_, err := newFoundryMemoryStoreEnsurer(nil, nil)
	require.ErrorContains(t, err, "project endpoint")

	_, err = newFoundryMemoryStoreEnsurer(&PromptAgentSettings{}, nil)
	require.ErrorContains(t, err, "project endpoint")
}
