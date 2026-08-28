// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserConfigResponseStateStoreRoundTrip(t *testing.T) {
	server := newInvokeUserConfigServer()
	client := newInvokeTestAzdClient(t, server)
	store := newUserConfigResponseStateStore(client)

	cursor := int64(0)
	want := savedBackgroundResponse{
		ResponseID:         "resp_123",
		LastSequenceNumber: &cursor,
		Status:             "in_progress",
		SessionID:          "sess_123",
		ConversationID:     "conv_123",
	}

	require.NoError(t, store.Save(t.Context(), "agent-a", want))
	got, err := store.Get(t.Context(), "agent-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want, *got)

	require.NoError(t, store.Delete(t.Context(), "agent-a"))
	got, err = store.Get(t.Context(), "agent-a")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestUserConfigResponseStateStoreCursorIsMonotonic(t *testing.T) {
	server := newInvokeUserConfigServer()
	client := newInvokeTestAzdClient(t, server)
	store := newUserConfigResponseStateStore(client)

	require.NoError(t, store.Save(t.Context(), "agent-a", savedBackgroundResponse{
		ResponseID:         "resp_123",
		LastSequenceNumber: new(int64(10)),
		Status:             "in_progress",
	}))
	require.NoError(t, store.Save(t.Context(), "agent-a", savedBackgroundResponse{
		ResponseID:         "resp_123",
		LastSequenceNumber: new(int64(5)),
		Status:             "completed",
	}))

	got, err := store.Get(t.Context(), "agent-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.LastSequenceNumber)
	assert.Equal(t, int64(10), *got.LastSequenceNumber)
	assert.Equal(t, "completed", got.Status)
}

func TestCleanupAgentStateForKey(t *testing.T) {
	t.Parallel()

	const (
		agentKey = "agent-key"
		otherKey = "other-agent-key"
	)
	server := newInvokeUserConfigServer()
	server.setJSON(t, configPath("sessions"), map[string]string{
		agentKey: "sess_123",
		otherKey: "sess_other",
	})
	server.setJSON(t, configPath("conversations"), map[string]string{
		agentKey: "conv_123",
		otherKey: "conv_other",
	})
	server.setJSON(t, backgroundResponsesConfigPath, map[string]savedBackgroundResponse{
		agentKey: {ResponseID: "resp_123"},
		otherKey: {ResponseID: "resp_other"},
	})
	client := newInvokeTestAzdClient(t, server)

	require.True(t, cleanupAgentStateForKey(t.Context(), client, agentKey))

	var sessions map[string]string
	server.getJSON(t, configPath("sessions"), &sessions)
	assert.NotContains(t, sessions, agentKey)
	assert.Equal(t, "sess_other", sessions[otherKey])

	var conversations map[string]string
	server.getJSON(t, configPath("conversations"), &conversations)
	assert.NotContains(t, conversations, agentKey)
	assert.Equal(t, "conv_other", conversations[otherKey])

	var responses map[string]savedBackgroundResponse
	server.getJSON(t, backgroundResponsesConfigPath, &responses)
	assert.NotContains(t, responses, agentKey)
	assert.Equal(t, "resp_other", responses[otherKey].ResponseID)
}
