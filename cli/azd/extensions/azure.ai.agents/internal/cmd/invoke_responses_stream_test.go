// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type terminalThenErrorReader struct {
	stream *strings.Reader
	err    error
}

func (r *terminalThenErrorReader) Read(p []byte) (int, error) {
	if r.stream.Len() > 0 {
		return r.stream.Read(p)
	}
	return 0, r.err
}

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
	}, "\n") + "\n"

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

func TestReadResponsesSSEBackgroundTerminalRequiresIdentity(t *testing.T) {
	t.Parallel()

	stream := "event: response.completed\n" +
		`data: {"response":{"status":"completed"},"sequence_number":1}` + "\n\n"

	var progress []responsesStreamProgress
	err := readResponsesSSE(t.Context(), strings.NewReader(stream), io.Discard, "agent", true,
		func(value responsesStreamProgress) error {
			progress = append(progress, value)
			return nil
		})

	require.ErrorIs(t, err, errResponsesStreamEndedBeforeIdentity)
	assert.Empty(t, progress)
}

func TestReadResponsesSSEDiscardsPartialFrameAtEOF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		stream       string
		wantOutput   string
		wantProgress int
	}{
		{
			name:   "unterminated frame",
			stream: `data: {"type":"response.output_text.delta","delta":"partial","sequence_number":1}` + "\n",
		},
		{
			name:         "blank-line-delimited text followed by EOF",
			stream:       `data: {"type":"response.output_text.delta","delta":"complete","sequence_number":1}` + "\n\n",
			wantOutput:   "[agent] complete\n",
			wantProgress: 1,
		},
		{
			name: "delimited text then unterminated frame",
			stream: `data: {"type":"response.output_text.delta","delta":"complete","sequence_number":1}` + "\n\n" +
				`data: {"type":"response.output_text.delta","delta":"partial","sequence_number":2}` + "\n",
			wantOutput:   "[agent] complete\n",
			wantProgress: 1,
		},
		{
			name:         "delimited event without text",
			stream:       `data: {"type":"response.in_progress","response":{"id":"resp_123"}}` + "\n\n",
			wantProgress: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			var progress []responsesStreamProgress
			err := readResponsesSSE(t.Context(), strings.NewReader(tt.stream), &output, "agent", false,
				func(value responsesStreamProgress) error {
					progress = append(progress, value)
					return nil
				})

			require.NoError(t, err)
			assert.Equal(t, tt.wantOutput, output.String())
			assert.Len(t, progress, tt.wantProgress)
		})
	}
}

func TestReadResponsesSSEFailedTerminalDoesNotRenderSnapshot(t *testing.T) {
	t.Parallel()

	stream := "event: response.failed\n" +
		`data: {"response":{"id":"resp_123","status":"failed",` +
		`"error":{"code":"runtime_error","message":"agent crashed"}},"sequence_number":4}` +
		"\n\n"

	var output bytes.Buffer
	var progress []responsesStreamProgress
	err := readResponsesSSE(t.Context(), strings.NewReader(stream), &output, "agent", true,
		func(value responsesStreamProgress) error {
			progress = append(progress, value)
			return nil
		})

	require.EqualError(t, err, "agent failed (runtime_error): agent crashed")
	assert.Empty(t, output.String())
	require.Len(t, progress, 1)
	assert.Equal(t, "resp_123", progress[0].ResponseID)
	require.NotNil(t, progress[0].Cursor)
	assert.Equal(t, int64(4), *progress[0].Cursor)
	assert.Equal(t, "failed", progress[0].Status)
	assert.True(t, progress[0].Terminal)
}

func TestReadResponsesSSEReturnsAfterTerminalEvent(t *testing.T) {
	t.Parallel()

	readerErr := errors.New("reader should not be called after terminal event")
	reader := &terminalThenErrorReader{
		stream: strings.NewReader(
			"event: response.output_text.delta\n" +
				`data: {"response":{"id":"resp_123","status":"in_progress"},"delta":"done","sequence_number":1}` +
				"\n\n" +
				"event: response.completed\n" +
				`data: {"response":{"id":"resp_123","status":"completed"},"sequence_number":2}` +
				"\n\n",
		),
		err: readerErr,
	}

	var progress []responsesStreamProgress
	var output bytes.Buffer
	err := readResponsesSSE(t.Context(), reader, &output, "agent", true,
		func(value responsesStreamProgress) error {
			progress = append(progress, value)
			return nil
		})

	require.NoError(t, err)
	require.Len(t, progress, 2)
	assert.True(t, progress[1].Terminal)
	assert.Equal(t, "[agent] done\n", output.String())
}

func TestReadResponsesSSEResumedTerminalUsesInitialIdentity(t *testing.T) {
	t.Parallel()

	stream := "event: response.completed\n" +
		`data: {"response":{"status":"completed"},"sequence_number":2}` + "\n\n"
	initial := &responsesStreamInitialState{
		ResponseID: "resp_123",
		Cursor:     new(int64(1)),
		Status:     "in_progress",
	}

	var progress []responsesStreamProgress
	err := readResponsesSSEWithInitialState(
		t.Context(),
		strings.NewReader(stream),
		io.Discard,
		"agent",
		true,
		initial,
		func(value responsesStreamProgress) error {
			progress = append(progress, value)
			return nil
		},
	)

	require.NoError(t, err)
	require.Len(t, progress, 1)
	assert.Equal(t, "resp_123", progress[0].ResponseID)
	assert.Equal(t, int64(2), *progress[0].Cursor)
	assert.Equal(t, "completed", progress[0].Status)
	assert.True(t, progress[0].Terminal)
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

func TestReadResponsesSSEValidatesIdentityBeforeSuppressingDuplicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		responseID string
		wantErr    string
	}{
		{
			name:       "same response ID",
			responseID: "resp_123",
		},
		{
			name:       "mismatched response ID",
			responseID: "resp_other",
			wantErr:    `Responses stream changed response ID from "resp_123" to "resp_other"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stream := "event: response.in_progress\n" +
				fmt.Sprintf(
					`data: {"response":{"id":%q,"status":"in_progress"},"sequence_number":1}`,
					tt.responseID,
				) + "\n\n"
			initial := &responsesStreamInitialState{
				ResponseID: "resp_123",
				Cursor:     new(int64(1)),
				Status:     "in_progress",
			}
			var progress []responsesStreamProgress
			err := readResponsesSSEWithInitialState(
				t.Context(),
				strings.NewReader(stream),
				io.Discard,
				"agent",
				false,
				initial,
				func(value responsesStreamProgress) error {
					progress = append(progress, value)
					return nil
				},
			)

			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Empty(t, progress)
		})
	}
}

func TestReadResponsesSSEEventSizeLimit(t *testing.T) {
	t.Parallel()

	const limit = 32
	tests := []struct {
		name    string
		stream  string
		wantErr bool
	}{
		{
			name:   "exact retained data limit",
			stream: "event: ignored\ndata: " + strings.Repeat("x", limit) + "\n\n",
		},
		{
			name:    "retained data exceeds limit",
			stream:  "event: ignored\ndata: " + strings.Repeat("x", limit+1) + "\n\n",
			wantErr: true,
		},
		{
			name:   "empty data fields count newline separators",
			stream: strings.Repeat("data:\n", limit+1) + "\n",
		},
		{
			name:    "many empty data fields exceed limit",
			stream:  strings.Repeat("data:\n", limit+2) + "\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := readResponsesSSEWithLimit(
				t.Context(),
				strings.NewReader(tt.stream),
				io.Discard,
				"agent",
				false,
				nil,
				limit,
			)
			if tt.wantErr {
				require.EqualError(t, err, "Responses SSE event exceeds 32 bytes")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
