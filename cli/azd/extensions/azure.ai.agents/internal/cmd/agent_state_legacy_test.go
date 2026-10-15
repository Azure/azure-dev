// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type legacyStateConfigServer struct {
	*invokeUserConfigServer
	reads atomic.Int32
}

func newLegacyStateConfigServer(t *testing.T, state legacyAgentState) *legacyStateConfigServer {
	t.Helper()
	data, err := json.Marshal(state)
	require.NoError(t, err)
	server := &legacyStateConfigServer{invokeUserConfigServer: newInvokeUserConfigServer()}
	server.values["extensions.ai-agents"] = data
	return server
}

func (s *legacyStateConfigServer) Get(
	ctx context.Context, req *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	s.reads.Add(1)
	return s.invokeUserConfigServer.Get(ctx, req)
}

func TestAgentStateLegacyUserConfigMigration(t *testing.T) {
	t.Parallel()
	const key = "example.com/agents/worker/versions/1/remote"
	legacy := legacyAgentState{
		Sessions:      map[string]string{key: "session-1", "other-agent": "other-session"},
		Conversations: map[string]string{key: "conversation-1"},
		Responses:     map[string]savedResponse{key: {ResponseID: "response-1"}},
		Invocations:   map[string]savedInvocation{key: {InvocationID: "invocation-1"}},
		StateStores:   map[string]string{key: "checkpoints"},
	}
	server := newLegacyStateConfigServer(t, legacy)
	client := newInvokeTestAzdClient(t, server)
	record := loadAgentStateForTest(t, client, key)
	require.NotNil(t, record)
	assert.Equal(t, "session-1", record.SessionID)
	assert.Equal(t, "conversation-1", record.ConversationID)
	assert.Equal(t, "response-1", record.Response.ResponseID)
	assert.Equal(t, "invocation-1", record.Invocation.InvocationID)
	assert.Equal(t, "checkpoints", record.StateStore)

	require.NoError(t, setAgentSpecificContextValue(t.Context(), client, "sessions", key, "new-session"))
	record = loadAgentStateForTest(t, client, key)
	require.NotNil(t, record)
	assert.Equal(t, "new-session", record.SessionID)
	assert.EqualValues(t, 1, server.reads.Load(), "existing records must not consult legacy storage")
	wantLegacy, err := json.Marshal(legacy)
	require.NoError(t, err)
	server.mu.Lock()
	assert.JSONEq(t, string(wantLegacy), string(server.values["extensions.ai-agents"]))
	server.mu.Unlock()
}

func TestAgentStateLegacyProjectFileMigration(t *testing.T) {
	t.Parallel()
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote", true: "local"}[local], func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			oldKey := "worker"
			key := buildAgentKey("https://example.com/projects/project", "worker", "", false)
			if local {
				oldKey = "worker-local"
				key = buildLocalAgentKey(DefaultPort, "worker", "", projectDir)
			}
			data, err := json.Marshal(map[string]any{
				"sessions":      map[string]string{oldKey: "older-session"},
				"conversations": map[string]string{oldKey: "old-conversation"},
				"invocations":   map[string]string{oldKey: "old-invocation"},
			})
			require.NoError(t, err)
			path := filepath.Join(projectDir, ".azure", "dev", ".foundry-agent.json")
			mustWriteFile(t, path, string(data))
			server := newLegacyStateConfigServer(t, legacyAgentState{
				Sessions: map[string]string{key: "newer-session"},
			})
			project := &helpersProjectServer{project: &azdext.ProjectConfig{Path: projectDir}}
			env := &testEnvironmentServiceServer{current: &azdext.Environment{Name: "dev"}}
			client := newInvokeTestAzdClientWithProject(t, server, project, env)

			record := loadAgentStateForTest(t, client, key)
			require.NotNil(t, record)
			assert.Equal(t, "newer-session", record.SessionID)
			assert.Equal(t, "old-conversation", record.ConversationID)
			assert.Equal(t, "old-invocation", record.Invocation.InvocationID)
			retained, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, data, retained)
		})
	}
}

func TestAgentStateLegacyDoesNotOverwriteNewState(t *testing.T) {
	t.Parallel()
	const key = "example.com/agents/worker/versions/1/remote"
	server := newLegacyStateConfigServer(t, legacyAgentState{
		Sessions: map[string]string{key: "old-session"},
	})
	client := newInvokeTestAzdClient(t, server)
	saveAgentStateForTest(t, client, key, agentStateRecord{SessionID: "new-session"})
	record := loadAgentStateForTest(t, client, key)
	require.NotNil(t, record)
	assert.Equal(t, "new-session", record.SessionID)
	assert.Zero(t, server.reads.Load())
}

func TestAgentStateLegacyDoesNotResurrectDeletedState(t *testing.T) {
	t.Parallel()
	const key = "example.com/agents/worker/versions/1/remote"
	for _, migrateFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete-before-read", true: "delete-after-migration"}[migrateFirst],
			func(t *testing.T) {
				server := newLegacyStateConfigServer(t, legacyAgentState{
					Sessions: map[string]string{key: "old-session"},
				})
				client := newInvokeTestAzdClient(t, server)
				if migrateFirst {
					require.NotNil(t, loadAgentStateForTest(t, client, key))
				}
				store, err := newAgentStateStoreForClient(client)
				require.NoError(t, err)
				require.NoError(t, store.delete(t.Context(), key))
				assert.Nil(t, loadAgentStateForTest(t, client, key))
				if migrateFirst {
					assert.EqualValues(t, 1, server.reads.Load())
				} else {
					assert.Zero(t, server.reads.Load())
				}
			})
	}
}

func TestAgentStateLegacyFieldDeletionDoesNotReimport(t *testing.T) {
	t.Parallel()
	const key = "example.com/agents/worker/versions/latest/remote"
	server := newLegacyStateConfigServer(t, legacyAgentState{
		Sessions: map[string]string{"worker": "old-session"},
	})
	client := newInvokeTestAzdClient(t, server)
	record := loadAgentStateForTest(t, client, key)
	require.NotNil(t, record)
	require.NoError(t, deleteContextValue(t.Context(), client, "sessions", key))
	assert.Nil(t, loadAgentStateForTest(t, client, key))
	assert.EqualValues(t, 1, server.reads.Load())
}

func TestAgentStateLegacyFailedMigrationCanRetry(t *testing.T) {
	t.Parallel()
	const key = "example.com/agents/worker/versions/latest/remote"
	server := newLegacyStateConfigServer(t, legacyAgentState{})
	server.values["extensions.ai-agents"] = []byte("{")
	client := newInvokeTestAzdClient(t, server)
	store, err := newAgentStateStoreForClient(client)
	require.NoError(t, err)
	_, err = store.get(t.Context(), key)
	require.ErrorContains(t, err, "read legacy agent state")
	_, err = os.Stat(store.legacyMarkerPath(key))
	assert.ErrorIs(t, err, os.ErrNotExist)

	server.mu.Lock()
	server.values["extensions.ai-agents"] = []byte(`{"sessions":{"worker":"recovered-session"}}`)
	server.mu.Unlock()
	record := loadAgentStateForTest(t, client, key)
	require.NotNil(t, record)
	assert.Equal(t, "recovered-session", record.SessionID)
}

func TestAgentStateLegacyRedactsCredentialBearingKey(t *testing.T) {
	t.Parallel()
	const legacyKey = "secret-user:secret-password@HOST.example/agents/worker/versions/1" +
		"?sig=secret-signature#secret-fragment/remote"
	key := buildRemoteAgentKeyFromEndpoint("https://host.example/agents/worker/versions/1")
	server := newLegacyStateConfigServer(t, legacyAgentState{
		Sessions: map[string]string{legacyKey: "old-session"},
	})
	client := newInvokeTestAzdClient(t, server)
	record := loadAgentStateForTest(t, client, key)
	require.NotNil(t, record)
	assert.Equal(t, key, record.AgentKey)
	store, err := newAgentStateStoreForClient(client)
	require.NoError(t, err)
	data, err := os.ReadFile(store.statePath(key))
	require.NoError(t, err)
	for _, secret := range []string{"secret-user", "secret-password", "secret-signature", "secret-fragment"} {
		assert.NotContains(t, string(data), secret)
	}
}
