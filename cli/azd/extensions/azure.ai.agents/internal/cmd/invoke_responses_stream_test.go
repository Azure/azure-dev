// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadResponsesSSEBackground(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		"event:response.created",
		`data:{"type":"response.created","response":{"id":"resp_123","status":"in_progress"},"sequence_number":0}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"hello","sequence_number":1}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_123","status":"completed"},"sequence_number":2}`,
		"",
	}, "\n")

	var output bytes.Buffer
	var progress []responsesStreamProgress
	err := readResponsesSSE(t.Context(), strings.NewReader(stream), &output, "agent", true,
		func(value responsesStreamProgress) error {
			progress = append(progress, value)
			return nil
		})

	require.NoError(t, err)
	assert.Equal(t, "[agent] hello\n", output.String())
	require.Len(t, progress, 3)
	assert.Equal(t, "resp_123", progress[0].ResponseID)
	require.NotNil(t, progress[0].Cursor)
	assert.Equal(t, int64(0), *progress[0].Cursor)
	assert.Equal(t, "in_progress", progress[1].Status)
	assert.True(t, progress[2].Terminal)
	assert.Equal(t, "completed", progress[2].Status)
}

func TestReadResponsesSSEDataOnlyAndMultiline(t *testing.T) {
	t.Parallel()

	stream := "data: {\"type\":\"response.completed\",\n" +
		"data: \"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[]},\n" +
		"data: \"sequence_number\":0}\n\n"

	var output bytes.Buffer
	err := readResponsesSSE(t.Context(), strings.NewReader(stream), &output, "agent", true, nil)
	require.NoError(t, err)
}

func TestReadResponsesSSEBackgroundRequiresTerminal(t *testing.T) {
	t.Parallel()

	stream := "event: response.created\n" +
		`data: {"response":{"id":"resp_123","status":"in_progress"},"sequence_number":0}` + "\n\n"

	err := readResponsesSSE(t.Context(), strings.NewReader(stream), &bytes.Buffer{}, "agent", true, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disconnected before reaching a terminal state")
}

func TestReadResponsesSSESuppressesDuplicateSequence(t *testing.T) {
	t.Parallel()

	stream := "event: response.output_text.delta\n" +
		`data: {"delta":"one","sequence_number":1}` + "\n\n" +
		"event: response.output_text.delta\n" +
		`data: {"delta":"duplicate","sequence_number":1}` + "\n\n" +
		"event: response.completed\n" +
		`data: {"response":{"id":"resp_1","status":"completed"},"sequence_number":2}` + "\n\n"

	var output bytes.Buffer
	err := readResponsesSSE(context.Background(), strings.NewReader(stream), &output, "agent", false, nil)
	require.NoError(t, err)
	assert.Equal(t, "[agent] one\n", output.String())
}
