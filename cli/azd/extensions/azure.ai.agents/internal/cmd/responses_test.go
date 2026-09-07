// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesCommandSubcommands(t *testing.T) {
	cmd := newResponsesCommand(nil)
	for _, name := range []string{"show", "follow", "cancel"} {
		child, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		assert.Equal(t, name, child.Name())
		assert.NotNil(t, child.Flags().Lookup("response-id"))
		assert.NotNil(t, child.Flags().Lookup("agent-endpoint"))
	}
}

func TestResponsesEndpointRejectsAgentSelector(t *testing.T) {
	cmd := newResponsesShowCommand(nil)
	cmd.SetArgs([]string{
		"--response-id", "resp_123",
		"--agent-name", "agent",
		"--agent-endpoint",
		"https://example.services.ai.azure.com/api/projects/project/agents/agent/endpoint/protocols/openai/responses?api-version=v1",
	})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be combined")
}

func TestPrintResponseSnapshotJSON(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, printResponseSnapshot(&output, responseSnapshotResult{
		snapshot: responsesSnapshot{ID: "resp_123", Status: "completed"},
		raw:      []byte(`{"id":"resp_123","status":"completed"}`),
	}, "json"))
	assert.JSONEq(t, `{"id":"resp_123","status":"completed"}`, output.String())
}

func TestPrintResponseSnapshotTable(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, printResponseSnapshot(&output, responseSnapshotResult{
		snapshot: responsesSnapshot{ID: "resp_123", Status: "completed", AgentSessionID: "sess_123"},
	}, "table"))
	assert.Contains(t, output.String(), "Response ID  resp_123")
	assert.Contains(t, output.String(), "Status       completed")
	assert.Contains(t, output.String(), "Session ID   sess_123")
}

func TestReadResponsesSSETracksProcessLocalCursor(t *testing.T) {
	stream := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"sequence_number\":0," +
		"\"response\":{\"id\":\"resp_123\",\"status\":\"queued\"}}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"sequence_number\":1," +
		"\"response\":{\"id\":\"resp_123\",\"status\":\"completed\"}}\n\n"
	var cursor *int64
	var output bytes.Buffer
	err := readResponsesSSE(t.Context(), bytes.NewBufferString(stream), &output, "agent", responsesSSEOptions{
		requireTerminal: true,
		onProgress: func(progress responsesStreamProgress) error {
			cursor = progress.Cursor
			return nil
		},
	})
	require.NoError(t, err)
	require.NotNil(t, cursor)
	assert.Equal(t, int64(1), *cursor)
}
