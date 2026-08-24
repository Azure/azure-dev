// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvocationStateStoreRoundTrip(t *testing.T) {
	server := newInvokeUserConfigServer()
	client := newInvokeTestAzdClient(t, server)
	store := newInvocationStateStore(client)
	want := savedInvocation{
		InvocationID: "inv_123",
		SessionID:    "sess_123",
		APIVersion:   "v1",
	}

	require.NoError(t, store.Save(t.Context(), "agent", want))
	got, err := store.Get(t.Context(), "agent")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want, *got)

	require.NoError(t, store.Delete(t.Context(), "agent"))
	got, err = store.Get(t.Context(), "agent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestBuildInvocationRetrievalURL(t *testing.T) {
	t.Parallel()

	got := buildInvocationRetrievalURL(
		"https://acct.services.ai.azure.com/api/projects/proj",
		"agent",
		"inv/a b",
		"v1",
		"session/a",
	)
	assert.Contains(t, got, "/invocations/inv%2Fa%20b?")
	assert.Contains(t, got, "api-version=v1")
	assert.Contains(t, got, "agent_session_id=session%2Fa")
}
