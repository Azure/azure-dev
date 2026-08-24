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
		ResponseID:     "resp_123",
		Cursor:         &cursor,
		Status:         "in_progress",
		SessionID:      "sess_123",
		ConversationID: "conv_123",
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
		ResponseID: "resp_123",
		Cursor:     new(int64(10)),
		Status:     "in_progress",
	}))
	require.NoError(t, store.Save(t.Context(), "agent-a", savedBackgroundResponse{
		ResponseID: "resp_123",
		Cursor:     new(int64(5)),
		Status:     "completed",
	}))

	got, err := store.Get(t.Context(), "agent-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Cursor)
	assert.Equal(t, int64(10), *got.Cursor)
	assert.Equal(t, "completed", got.Status)
}
