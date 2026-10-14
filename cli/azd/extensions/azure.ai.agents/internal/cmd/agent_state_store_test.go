// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentStateStoreRoundTrip(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)

	require.NoError(t, store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
		record.SessionID = "session-1"
		record.ConversationID = "conversation-1"
		record.Response = &savedResponse{ResponseID: "response-1"}
		record.Invocation = &savedInvocation{InvocationID: "invocation-1"}
		record.StateStore = "checkpoints"
	}))

	record, err := store.get(t.Context(), "agent-a")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "session-1", record.SessionID)
	assert.Equal(t, "conversation-1", record.ConversationID)
	assert.Equal(t, "response-1", record.Response.ResponseID)
	assert.Equal(t, "invocation-1", record.Invocation.InvocationID)
	assert.Equal(t, "checkpoints", record.StateStore)

	data, err := os.ReadFile(store.statePath("agent-a"))
	require.NoError(t, err)
	var persisted agentStateRecord
	require.NoError(t, json.Unmarshal(data, &persisted))
	assert.Equal(t, "agent-a", persisted.AgentKey)
	assert.NotContains(t, store.statePath("agent-a"), "agent-a")
}

func TestAgentStateStoreConcurrentFieldUpdates(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Go(func() {
		require.NoError(t, store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
			record.SessionID = "session-1"
		}))
	})
	wg.Go(func() {
		require.NoError(t, store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
			record.ConversationID = "conversation-1"
		}))
	})
	wg.Go(func() {
		require.NoError(t, store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
			record.Response = &savedResponse{ResponseID: "response-1"}
		}))
	})
	wg.Wait()

	record, err := store.get(t.Context(), "agent-a")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "session-1", record.SessionID)
	assert.Equal(t, "conversation-1", record.ConversationID)
	assert.Equal(t, "response-1", record.Response.ResponseID)
}

func TestAgentStateStoreDeletesEmptyRecord(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
		record.SessionID = "session-1"
	}))
	require.NoError(t, store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
		record.SessionID = ""
	}))

	_, err = os.Stat(store.statePath("agent-a"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	record, err := store.get(t.Context(), "agent-a")
	require.NoError(t, err)
	assert.Nil(t, record)
}

func TestAgentStateStoreRejectsCorruptState(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.statePath("agent-a"), []byte("{"), 0o600))

	_, err = store.get(t.Context(), "agent-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse agent state")
}

func TestAgentStateStoreRejectsNonRegularLock(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(store.lockPath("agent-a"), 0o700))

	_, err = store.get(t.Context(), "agent-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a regular file")
}

func TestAgentStateStoreDoesNotUseUserConfig(t *testing.T) {
	t.Parallel()

	client := newInvokeTestAzdClient(t, &azdext.UnimplementedUserConfigServiceServer{})
	const agentKey = "example.com/agents/worker/versions/1/remote"
	for field, value := range map[string]string{
		"sessions": "session-1", "conversations": "conversation-1", "stateStores": "checkpoints",
	} {
		require.NoError(t, setAgentSpecificContextValue(t.Context(), client, field, agentKey, value))
		got, err := getAgentSpecificContextValue(t.Context(), client, field, agentKey)
		require.NoError(t, err)
		assert.Equal(t, value, got)
	}

	responses := newResponseStateStore(client)
	require.NoError(t, responses.Save(t.Context(), agentKey, savedResponse{ResponseID: "response-1"}))
	response, err := responses.Get(t.Context(), agentKey)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "response-1", response.ResponseID)

	invocations := newInvocationStateStore(client)
	require.NoError(t, invocations.Save(t.Context(), agentKey, savedInvocation{InvocationID: "invocation-1"}))
	invocation, err := invocations.Get(t.Context(), agentKey)
	require.NoError(t, err)
	require.NotNil(t, invocation)
	assert.Equal(t, "invocation-1", invocation.InvocationID)

	for _, field := range []string{"sessions", "conversations", "stateStores"} {
		require.NoError(t, deleteContextValue(t.Context(), client, field, agentKey))
	}
	require.NoError(t, responses.Delete(t.Context(), agentKey))
	require.NoError(t, invocations.Delete(t.Context(), agentKey))
	assert.Nil(t, loadAgentStateForTest(t, client, agentKey))
}

func TestResolveStoredIDFromAgentState(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		saved      string
		explicit   string
		forceNew   bool
		generate   bool
		want       string
		wantStored string
	}{
		{name: "reuse", saved: "saved-session", want: "saved-session", wantStored: "saved-session"},
		{name: "explicit", saved: "saved-session", explicit: "explicit-session",
			want: "explicit-session", wantStored: "explicit-session"},
		{name: "new remote session", saved: "saved-session", forceNew: true, wantStored: "saved-session"},
		{name: "missing remote session"},
		{name: "generate local session", generate: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newInvokeTestAzdClient(t, &azdext.UnimplementedUserConfigServiceServer{})
			const agentKey = "example.com/agents/worker/versions/1/remote"
			if tt.saved != "" {
				saveAgentStateForTest(t, client, agentKey, agentStateRecord{SessionID: tt.saved})
			}
			got, err := resolveStoredID(t.Context(), client, agentKey, tt.explicit, tt.forceNew, "sessions", tt.generate)
			require.NoError(t, err)
			wantStored := tt.wantStored
			if tt.generate {
				require.Len(t, got, 36)
				wantStored = got
			} else {
				assert.Equal(t, tt.want, got)
			}
			stored, err := getAgentSpecificContextValue(t.Context(), client, "sessions", agentKey)
			require.NoError(t, err)
			assert.Equal(t, wantStored, stored)
		})
	}
}
