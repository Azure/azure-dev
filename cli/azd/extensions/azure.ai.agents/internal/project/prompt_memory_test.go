// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/pkg/recordproxy"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestResolvePromptMemoryModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		bindings       string
		chatModel      string
		embeddingModel string
		wantChat       string
		wantEmbedding  string
		wantSkipped    string
	}{
		{
			name:          "reuse differently named live deployments",
			bindings:      `{"authored-chat":"live-chat","authored-embedding":"live-embedding"}`,
			wantChat:      "live-chat",
			wantEmbedding: "live-embedding",
		},
		{
			name:          "replacement deployments override the legacy default",
			bindings:      `{"authored-chat":"replacement-chat","authored-embedding":"replacement-embedding"}`,
			wantChat:      "replacement-chat",
			wantEmbedding: "replacement-embedding",
		},
		{
			name:        "skipped chat declaration",
			bindings:    `{"authored-chat":"","authored-embedding":"live-embedding"}`,
			wantSkipped: "authored-chat",
		},
		{
			name:        "skipped embedding declaration",
			bindings:    `{"authored-chat":"live-chat","authored-embedding":""}`,
			wantSkipped: "authored-embedding",
		},
		{
			name:           "environment references resolve through bindings",
			bindings:       `{"authored-chat":"live-chat","authored-embedding":"live-embedding"}`,
			chatModel:      "${MEMORY_CHAT}",
			embeddingModel: "${MEMORY_EMBEDDING}",
			wantChat:       "live-chat",
			wantEmbedding:  "live-embedding",
		},
		{
			name:          "unchanged deployment bindings",
			bindings:      `{"authored-chat":"authored-chat","authored-embedding":"authored-embedding"}`,
			wantChat:      "authored-chat",
			wantEmbedding: "authored-embedding",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			agent, services := promptModelServices(t, "authored-chat", "authored-embedding")
			memory := &agent_yaml.PromptMemory{
				Store: "conversation-memory", ChatModel: "authored-chat", EmbeddingModel: "authored-embedding",
			}
			if tt.chatModel != "" {
				memory.ChatModel = tt.chatModel
			}
			if tt.embeddingModel != "" {
				memory.EmbeddingModel = tt.embeddingModel
			}
			env := map[string]string{
				"AI_PROJECT_DEPLOYMENT_BINDINGS": tt.bindings,
				"AI_PROJECT_DEPLOYMENTS":         "[]",
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "unrelated-default",
				"MEMORY_CHAT":                    "authored-chat",
				"MEMORY_EMBEDDING":               "authored-embedding",
			}
			err := resolvePromptMemoryModels(memory, agent, services, t.TempDir(), env)
			if tt.wantSkipped != "" {
				require.ErrorContains(t, err, "model deployment \""+tt.wantSkipped+"\" was skipped")
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, exterrors.CodeModelDeploymentNotFound, localErr.Code)
				return
			}
			require.NoError(t, err)
			g, fake, node := newMemoryTestGraph(memory)
			require.Same(t, memory, g.managed.Memory)
			require.Equal(t, tt.wantChat, memory.ChatModel)
			require.Equal(t, tt.wantEmbedding, memory.EmbeddingModel)
			require.NoError(t, node.Validate())
			require.NoError(t, node.Resolve(t.Context()))
			require.NotNil(t, fake.request)
			require.Equal(t, tt.wantChat, fake.request.Definition.ChatModel)
			require.Equal(t, tt.wantEmbedding, fake.request.Definition.EmbeddingModel)
		})
	}
}

type promptMemoryEnvServer struct {
	stubEnvServer
}

func (s *promptMemoryEnvServer) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	kvs := make([]*azdext.KeyValue, 0, len(s.values))
	for k, v := range s.values {
		kvs = append(kvs, &azdext.KeyValue{Key: k, Value: v})
	}
	return &azdext.KeyValueListResponse{KeyValues: kvs}, nil
}

func TestDeployPromptAgentReconcilesMemoryModels(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"

	tests := []struct {
		name          string
		bindings      string
		chatModel     string
		wantChat      string
		wantEmbedding string
		wantSkipped   string
	}{
		{
			name:          "reconciled bindings flow into memory store and agent requests",
			bindings:      `{"authored-agent":"live-agent","authored-chat":"live-chat","authored-embedding":"replacement-embedding"}`,
			chatModel:     "${MEMORY_CHAT}",
			wantChat:      "live-chat",
			wantEmbedding: "replacement-embedding",
		},
		{
			name:        "skipped chat binding fails before memory store requests",
			bindings:    `{"authored-agent":"live-agent","authored-chat":"","authored-embedding":"live-embedding"}`,
			chatModel:   "authored-chat",
			wantSkipped: "authored-chat",
		},
		{
			name:        "skipped embedding binding fails before memory store requests",
			bindings:    `{"authored-agent":"live-agent","authored-chat":"live-chat","authored-embedding":""}`,
			chatModel:   "authored-chat",
			wantSkipped: "authored-embedding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, services := promptModelServices(
				t, "authored-agent", "authored-chat", "authored-embedding",
			)
			props, err := structpb.NewStruct(map[string]any{
				"kind":         "prompt",
				"model":        "authored-agent",
				"instructions": "Remember customer context.",
				"memory": map[string]any{
					"store":          "conversation-memory",
					"chatModel":      tt.chatModel,
					"embeddingModel": "authored-embedding",
				},
			})
			require.NoError(t, err)
			agent.AdditionalProperties = props

			envServer := &promptMemoryEnvServer{
				stubEnvServer: stubEnvServer{
					values: map[string]string{
						"FOUNDRY_PROJECT_ENDPOINT":       endpoint,
						"AZURE_SUBSCRIPTION_ID":          "00000000-0000-0000-0000-000000000001",
						"AZURE_RESOURCE_GROUP":           "rg-test",
						"AI_PROJECT_DEPLOYMENT_BINDINGS": tt.bindings,
						"AI_PROJECT_DEPLOYMENTS":         "[]",
						"AZURE_AI_MODEL_DEPLOYMENT_NAME": "unrelated-default",
						"MEMORY_CHAT":                    "authored-chat",
					},
				},
			}
			provider := &AgentServiceTargetProvider{
				azdClient:       newEnvTestClient(t, envServer),
				env:             &azdext.Environment{Name: "test-env"},
				serviceConfig:   agent,
				projectServices: services,
				projectPath:     t.TempDir(),
				credential:      hostedResourceErrorCredential{},
			}

			var requests []string
			var storeReq azure.CreateMemoryStoreRequest
			var agentReq map[string]any

			original := recordproxy.Transport
			t.Cleanup(func() { recordproxy.Transport = original })
			recordproxy.Transport = hostedResourceErrorTransport(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.Method+" "+req.URL.Path)
				switch {
				case req.Method == http.MethodGet &&
					req.URL.Path == "/api/projects/project/memory_stores/conversation-memory":
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"NotFound"}}`)),
						Request:    req,
					}, nil
				case req.Method == http.MethodPost &&
					req.URL.Path == "/api/projects/project/memory_stores":
					body, readErr := io.ReadAll(req.Body)
					require.NoError(t, readErr)
					require.NoError(t, json.Unmarshal(body, &storeReq))
					return &http.Response{
						StatusCode: http.StatusCreated,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(
							`{"id":"store-1","name":"conversation-memory"}`,
						)),
						Request: req,
					}, nil
				case req.Method == http.MethodGet &&
					req.URL.Path == "/api/projects/project/agents/agent":
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"NotFound"}}`)),
						Request:    req,
					}, nil
				case req.Method == http.MethodPost &&
					req.URL.Path == "/api/projects/project/agents":
					body, readErr := io.ReadAll(req.Body)
					require.NoError(t, readErr)
					require.NoError(t, json.Unmarshal(body, &agentReq))
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(
							`{"name":"agent","versions":{"latest":{"version":"1","status":"active"}}}`,
						)),
						Request: req,
					}, nil
				default:
					return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.String())
				}
			})

			_, err = provider.deployPromptAgent(t.Context(), agent, nil)
			if tt.wantSkipped != "" {
				require.ErrorContains(t, err, "model deployment \""+tt.wantSkipped+"\" was skipped")
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, exterrors.CodeModelDeploymentNotFound, localErr.Code)
				require.Empty(t, requests)
				require.Empty(t, envServer.writes)
				return
			}

			require.NoError(t, err)
			require.Equal(t, []string{
				"GET /api/projects/project/memory_stores/conversation-memory",
				"POST /api/projects/project/memory_stores",
				"GET /api/projects/project/agents/agent",
				"POST /api/projects/project/agents",
			}, requests)
			require.Equal(t, "conversation-memory", storeReq.Name)
			require.Equal(t, tt.wantChat, storeReq.Definition.ChatModel)
			require.Equal(t, tt.wantEmbedding, storeReq.Definition.EmbeddingModel)
			def, ok := agentReq["definition"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "live-agent", def["model"])
		})
	}
}

func TestResolvePromptMemoryModelsWithoutMemory(t *testing.T) {
	t.Parallel()

	require.NoError(t, resolvePromptMemoryModels(nil, nil, nil, "", map[string]string{
		"AI_PROJECT_DEPLOYMENT_BINDINGS": "{",
	}))
}

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
			wantErr: "memory.chatModel",
		},
		{
			name:    "missing embedding model",
			memory:  agent_yaml.PromptMemory{Store: "s", ChatModel: "c"},
			wantErr: "memory.embeddingModel",
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
