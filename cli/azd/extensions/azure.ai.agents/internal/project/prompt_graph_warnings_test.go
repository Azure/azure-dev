// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

	"github.com/stretchr/testify/require"
)

// captureWarnings wires a warning sink onto the graph and returns the collected
// messages.
func captureWarnings(g *promptGraph) *[]string {
	var warnings []string
	g.warn = func(message string) { warnings = append(warnings, message) }
	return &warnings
}

// TestMemoryNode_ReportsDrift covers the case that motivated the check: the
// manifest is edited, the store already exists, and the edit therefore does
// nothing. The deploy still succeeds, so without a warning the manifest and the
// live resource disagree silently and forever.
func TestMemoryNode_ReportsDrift(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		declared    agent_yaml.PromptMemory
		live        azure.MemoryStoreDefinition
		wantWarning []string
	}{
		{
			name: "chat model drifted",
			declared: agent_yaml.PromptMemory{
				Store: "m", ChatModel: "gpt-4.1", EmbeddingModel: "text-embedding-3-small",
			},
			live: azure.MemoryStoreDefinition{
				ChatModel: "gpt-4o", EmbeddingModel: "text-embedding-3-small",
			},
			wantWarning: []string{`chat_model (declared "gpt-4.1", current "gpt-4o")`},
		},
		{
			name: "both drifted",
			declared: agent_yaml.PromptMemory{
				Store: "m", ChatModel: "gpt-4.1", EmbeddingModel: "text-embedding-3-large",
			},
			live: azure.MemoryStoreDefinition{
				ChatModel: "gpt-4o", EmbeddingModel: "text-embedding-3-small",
			},
			wantWarning: []string{
				`chat_model (declared "gpt-4.1", current "gpt-4o")`,
				`embedding_model (declared "text-embedding-3-large", current "text-embedding-3-small")`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			memory := test.declared
			g, fake, node := newMemoryTestGraph(&memory)
			warnings := captureWarnings(g)

			// created=false is what fakeMemoryStoreEnsurer returns when a store
			// is pre-seeded, which is exactly the reuse path under test.
			fake.store = &azure.MemoryStoreObject{
				Name: memory.Store, Id: "store-1", Definition: test.live,
			}

			require.NoError(t, node.Resolve(t.Context()))

			require.Len(t, *warnings, 1)
			for _, fragment := range test.wantWarning {
				require.Contains(t, (*warnings)[0], fragment)
			}
			require.Contains(t, (*warnings)[0], "never updated")

			// The drift is reported, not enforced: the tool is still wired up so
			// the deploy produces a working agent.
			require.Equal(t, memory.Store, g.bindings[memoryStoreBindingKey])
		})
	}
}

// TestMemoryNode_NoDriftWarning verifies the check stays quiet when it should,
// so the warning keeps its signal.
func TestMemoryNode_NoDriftWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		live azure.MemoryStoreDefinition
	}{
		{
			name: "definitions match",
			live: azure.MemoryStoreDefinition{ChatModel: "gpt-4.1", EmbeddingModel: "embed"},
		},
		{
			// A service that does not echo the definition back is not evidence
			// of drift, and treating it as such would warn on every deploy.
			name: "service returned no definition",
			live: azure.MemoryStoreDefinition{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			memory := agent_yaml.PromptMemory{
				Store: "m", ChatModel: "gpt-4.1", EmbeddingModel: "embed",
			}
			g, fake, node := newMemoryTestGraph(&memory)
			warnings := captureWarnings(g)
			fake.store = &azure.MemoryStoreObject{Name: "m", Definition: test.live}

			require.NoError(t, node.Resolve(t.Context()))
			require.Empty(t, *warnings)
		})
	}
}

// TestMemoryNode_NewStoreNeverWarnsDrift verifies a freshly created store is not
// compared against itself.
func TestMemoryNode_NewStoreNeverWarnsDrift(t *testing.T) {
	t.Parallel()

	memory := agent_yaml.PromptMemory{Store: "m", ChatModel: "gpt-4.1", EmbeddingModel: "embed"}
	g, _, node := newMemoryTestGraph(&memory)
	warnings := captureWarnings(g)

	// The fake reports created=true with an empty definition when no store is
	// pre-seeded, which would look like drift if creation were not excluded.
	require.NoError(t, node.Resolve(t.Context()))
	require.Empty(t, *warnings)
}

// TestPromptGraph_WarnfIsSafeWithoutSink verifies warnings outside a resolve are
// dropped rather than panicking.
func TestPromptGraph_WarnfIsSafeWithoutSink(t *testing.T) {
	t.Parallel()

	g := &promptGraph{bindings: map[string]any{}}
	require.NotPanics(t, func() { g.warnf("anything %s", "here") })
}

// TestAgentNode_RejectsMalformedTools verifies tool validation runs in the
// validate phase, before anything is provisioned.
func TestAgentNode_RejectsMalformedTools(t *testing.T) {
	t.Parallel()

	managed := &agent_yaml.PromptAgent{
		Model:        "gpt-4.1-mini",
		Instructions: "You are helpful.",
		Tools:        []any{map[string]any{"server_label": "toolbox"}},
	}
	managed.Name = "agent-1"

	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	err := g.agentNode().Validate()

	require.Error(t, err)
	require.Contains(t, err.Error(), "missing a 'type' key")
}

// TestAgentNode_AllowsUnrecognizedToolType verifies an unfamiliar tool type is
// not a hard failure. `tools:` is pass-through so authors can use service
// features newer than their azd build; failing here would make every new tool
// type a breaking change.
func TestAgentNode_AllowsUnrecognizedToolType(t *testing.T) {
	t.Parallel()

	managed := &agent_yaml.PromptAgent{
		Model:        "gpt-4.1-mini",
		Instructions: "You are helpful.",
		Tools:        []any{map[string]any{"type": "something_new_preview"}},
	}
	managed.Name = "agent-1"

	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	require.NoError(t, g.agentNode().Validate())

	unrecognized := managed.UnrecognizedToolTypes()
	require.Equal(t, []string{"something_new_preview"}, unrecognized)
}

func TestPluralize(t *testing.T) {
	t.Parallel()

	require.Equal(t, "type", pluralize("type", 1))
	require.Equal(t, "types", pluralize("type", 2))
	require.Equal(t, "types", pluralize("type", 0))
}

// TestPromptGraph_WarnsOnUnrecognizedToolTypes exercises the warning end to end
// through resolve, including that it reaches the progress reporter.
func TestPromptGraph_WarnsOnUnrecognizedToolTypes(t *testing.T) {
	t.Parallel()

	managed := &agent_yaml.PromptAgent{
		Model:        "gpt-4.1-mini",
		Instructions: "You are helpful.",
		Tools: []any{
			map[string]any{"type": "file_search"},
			map[string]any{"type": "file_serach"},
		},
	}
	managed.Name = "agent-1"

	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	g.nodes = append(g.nodes, g.agentNode())

	var messages []string
	require.NoError(t, g.resolve(t.Context(), func(message string) { messages = append(messages, message) }))

	joined := strings.Join(messages, "\n")
	require.Contains(t, joined, "Warning:")
	require.Contains(t, joined, "file_serach")
	require.NotContains(t, joined, "file_search,", "the correctly spelled tool should not be flagged")

	// The sink is cleared once resolve returns, so a later stray warning cannot
	// be attributed to a deploy that already finished.
	require.Nil(t, g.warn)
}
