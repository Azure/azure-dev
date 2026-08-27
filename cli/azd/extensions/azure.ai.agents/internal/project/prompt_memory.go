// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"
)

// memorySearchToolType is the wire `type` of the tool that lets an agent recall
// from a memory store. The `_preview` suffix is part of the contract, not a
// description of it: the API previously defined a plain "memory_search" type
// and removed it in v1, so dropping the suffix names a type the service no
// longer recognizes — and an unrecognized tool is ignored without error.
const memorySearchToolType = "memory_search_preview"

// memoryStoreBindingKey is the graph binding under which the resolved memory
// store name is published for later nodes / observability.
const memoryStoreBindingKey = "memory_store_name"

// memoryStoreEnsurer creates a memory store if it does not already exist and
// returns the live store. Implementations are idempotent. The seam keeps the
// graph node unit-testable without a live endpoint.
type memoryStoreEnsurer interface {
	EnsureMemoryStore(
		ctx context.Context, request *azure.CreateMemoryStoreRequest,
	) (store *azure.MemoryStoreObject, created bool, err error)
}

// memoryNode builds the memory_store graph node for a declared `memory:` block.
// It ensures the store exists and then injects the memory_search_preview tool
// that actually connects the agent to it. Returns nil when no memory is
// declared (the caller then registers no node).
//
// Both halves live in one node on purpose. The store and the tool are useless
// apart — a store nothing reads from, or a tool pointing at a store that does
// not exist — so they succeed or fail together.
func memoryNode(
	g *promptGraph,
	memory *agent_yaml.PromptMemory,
	newEnsurer func() (memoryStoreEnsurer, error),
) *promptNode {
	if memory == nil {
		return nil
	}
	return &promptNode{
		Kind: nodeMemoryStore,
		ID:   strings.TrimSpace(memory.Store),
		Validate: func() error {
			if strings.TrimSpace(memory.Store) == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					"memory requires a store name",
					"set 'memory.store' in agent.yaml to the name of the memory store to use "+
						"(e.g. store: conversation-memory)",
				)
			}
			// The store is created if missing, and creation needs both models.
			// Requiring them up front beats discovering it mid-deploy, after the
			// model deployment and vector store have already been provisioned.
			missing := make([]string, 0, 2)
			if strings.TrimSpace(memory.ChatModel) == "" {
				missing = append(missing, "memory.chat_model")
			}
			if strings.TrimSpace(memory.EmbeddingModel) == "" {
				missing = append(missing, "memory.embedding_model")
			}
			if len(missing) > 0 {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					fmt.Sprintf("memory store %q requires %s", memory.Store, strings.Join(missing, " and ")),
					"set them to model deployment names declared under your azure.ai.project service; "+
						"the chat model summarizes conversations and the embedding model indexes memories",
				)
			}
			return nil
		},
		Resolve: func(ctx context.Context) error {
			ensurer, err := newEnsurer()
			if err != nil {
				return err
			}

			store, created, err := ensurer.EnsureMemoryStore(ctx, memoryStoreRequest(memory))
			if err != nil {
				return fmt.Errorf("ensuring memory store %q: %w", memory.Store, err)
			}

			// Prefer the service's name over the declared one so the tool
			// references the store as the service actually recorded it.
			name := strings.TrimSpace(store.Name)
			if name == "" {
				name = strings.TrimSpace(memory.Store)
			}

			if !created {
				reportMemoryStoreDrift(g, name, memory, store)
			}

			g.bindings[memoryStoreBindingKey] = name
			injectMemorySearchTool(g.managed, name, memory)
			return nil
		},
	}
}

// reportMemoryStoreDrift warns when a reused store's live definition differs
// from what agent.yaml declares.
//
// Memory stores are created-if-missing and never updated, so editing
// memory.chat_model in a manifest whose store already exists has no effect. The
// deploy still succeeds, which is the problem: without this, the manifest and
// the resource disagree silently and indefinitely. Warning rather than failing
// keeps a store shared with another agent — whose definition this manifest does
// not own — from blocking the deploy.
//
// The comparison is shared with the azure.yaml memoryStores: path. agent.yaml
// keys match the wire field paths, so no label mapping is needed.
func reportMemoryStoreDrift(
	g *promptGraph,
	storeName string,
	declared *agent_yaml.PromptMemory,
	live *azure.MemoryStoreObject,
) {
	drifted := describeMemoryStoreDrift(
		diffMemoryStoreDefinition(memoryStoreDefinition(declared), live.Definition),
		nil,
	)
	if len(drifted) == 0 {
		return
	}

	g.warnf(
		"memory store %q already exists and its %s. "+
			"Existing stores are reused as-is and never updated, so the declared value has no effect. "+
			"Delete the store, or point 'memory.store' at a new name, to apply it.",
		storeName,
		strings.Join(drifted, "; and its "),
	)
}

// memoryStoreRequest translates the authored memory block into the create
// request for the memory store resource.
func memoryStoreRequest(memory *agent_yaml.PromptMemory) *azure.CreateMemoryStoreRequest {
	return &azure.CreateMemoryStoreRequest{
		Name:        strings.TrimSpace(memory.Store),
		Description: memory.Description,
		Definition:  memoryStoreDefinition(memory),
	}
}

// memoryStoreDefinition translates the authored memory block into the wire
// definition. It is split out from memoryStoreRequest so the drift check
// compares the exact definition that creation would have sent, rather than a
// second, hand-maintained projection of the same fields.
func memoryStoreDefinition(memory *agent_yaml.PromptMemory) azure.MemoryStoreDefinition {
	definition := azure.MemoryStoreDefinition{
		Kind:           azure.MemoryStoreKindDefault,
		ChatModel:      strings.TrimSpace(memory.ChatModel),
		EmbeddingModel: strings.TrimSpace(memory.EmbeddingModel),
	}

	if memory.Options != nil {
		definition.Options = memoryStoreOptionsOrNil(&azure.MemoryStoreOptions{
			ChatSummaryEnabled:      memory.Options.ChatSummaryEnabled,
			UserProfileEnabled:      memory.Options.UserProfileEnabled,
			ProceduralMemoryEnabled: memory.Options.ProceduralMemoryEnabled,
			DefaultTTLSeconds:       memory.Options.DefaultTTLSeconds,
			UserProfileDetails:      memory.Options.UserProfileDetails,
		})
	}

	return definition
}

// injectMemorySearchTool ensures the agent's tools include a
// memory_search_preview tool bound to storeName. An existing entry is updated in
// place rather than duplicated, so re-deploying does not accumulate tools. The
// managed definition is mutated in place.
func injectMemorySearchTool(managed *agent_yaml.PromptAgent, storeName string, memory *agent_yaml.PromptMemory) {
	if managed == nil || memory == nil || strings.TrimSpace(storeName) == "" {
		return
	}

	scope := strings.TrimSpace(memory.Scope)
	if scope == "" {
		// Default to per-caller isolation. A shared default would let one user's
		// memories surface in another user's conversation.
		scope = agent_yaml.DefaultMemoryScope
	}

	tool := map[string]any{
		"type":              memorySearchToolType,
		"memory_store_name": storeName,
		"scope":             scope,
	}
	if memory.UpdateDelay != nil {
		tool["update_delay"] = *memory.UpdateDelay
	}
	if memory.MaxMemories != nil {
		tool["search_options"] = map[string]any{"max_memories": *memory.MaxMemories}
	}

	for i, raw := range managed.Tools {
		existing, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", existing["type"]) != memorySearchToolType {
			continue
		}
		managed.Tools[i] = tool
		return
	}

	managed.Tools = append(managed.Tools, tool)
}

// newFoundryMemoryStoreEnsurer constructs the live ensurer from prompt settings.
// It requires a resolved project endpoint (data-plane) to reach the memory
// stores API.
func newFoundryMemoryStoreEnsurer(settings *PromptAgentSettings) (memoryStoreEnsurer, error) {
	if settings == nil || strings.TrimSpace(settings.ProjectEndpoint) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"a Foundry project endpoint is required to provision a memory store",
			"run `azd up` to provision a Foundry project, or remove the 'memory:' block from agent.yaml",
		)
	}
	return azure.NewFoundryMemoryStoreClient(settings.ProjectEndpoint, promptCredential()), nil
}
