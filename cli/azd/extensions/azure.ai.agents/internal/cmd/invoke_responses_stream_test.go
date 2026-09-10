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

func readResponsesSSEForTest(
	ctx context.Context,
	body io.Reader,
	writer io.Writer,
	agentName string,
	requireTerminal bool,
	onResponseID func(string) error,
) error {
	return readResponsesSSE(ctx, body, writer, agentName, responsesSSEOptions{
		requireTerminal: requireTerminal,
		onResponseID:    onResponseID,
	})
}

func readResponsesSSEWithExpectedIDForTest(
	ctx context.Context,
	body io.Reader,
	writer io.Writer,
	agentName string,
	requireTerminal bool,
	expectedResponseID string,
	onResponseID func(string) error,
) error {
	return readResponsesSSE(ctx, body, writer, agentName, responsesSSEOptions{
		requireTerminal:    requireTerminal,
		expectedResponseID: expectedResponseID,
		onResponseID:       onResponseID,
	})
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
	var responseIDs []string
	err := readResponsesSSEForTest(t.Context(), strings.NewReader(stream), &output, "agent", true,
		func(responseID string) error {
			responseIDs = append(responseIDs, responseID)
			return nil
		})

	require.NoError(t, err)
	assert.Equal(t, "[agent] hello\n", output.String())
	assert.Equal(t, []string{"resp_123"}, responseIDs)
}

func TestReadResponsesSSEDataOnlyAndMultiline(t *testing.T) {
	t.Parallel()

	stream := "data: {\"type\":\"response.completed\",\n" +
		"data: \"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[]},\n" +
		"data: \"sequence_number\":0}\n\n"

	var output bytes.Buffer
	err := readResponsesSSEForTest(t.Context(), strings.NewReader(stream), &output, "agent", true, nil)
	require.NoError(t, err)
}

func TestReadResponsesSSEMalformedEventHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stream  string
		wantErr bool
	}{
		{
			name:    "data-only event",
			stream:  "data: {not-json}\n\n",
			wantErr: true,
		},
		{
			name:   "explicit unknown event",
			stream: "event: response.future_extension\ndata: {not-json}\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := readResponsesSSEForTest(t.Context(), strings.NewReader(tt.stream), io.Discard, "agent", false, nil)
			if tt.wantErr {
				require.ErrorContains(t, err, "decode Responses SSE event")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestReadResponsesSSEBackgroundRequiresTerminal(t *testing.T) {
	t.Parallel()

	stream := "event: response.created\n" +
		`data: {"response":{"id":"resp_123","status":"in_progress"},"sequence_number":0}` + "\n\n"

	err := readResponsesSSEForTest(t.Context(), strings.NewReader(stream), &bytes.Buffer{}, "agent", true, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disconnected before reaching a terminal state")
}

func TestReadResponsesSSEBackgroundTerminalRequiresIdentity(t *testing.T) {
	t.Parallel()

	stream := "event: response.completed\n" +
		`data: {"response":{"status":"completed"},"sequence_number":1}` + "\n\n"

	var responseIDs []string
	err := readResponsesSSEForTest(t.Context(), strings.NewReader(stream), io.Discard, "agent", true,
		func(responseID string) error {
			responseIDs = append(responseIDs, responseID)
			return nil
		})

	require.ErrorIs(t, err, errResponsesStreamEndedBeforeIdentity)
	assert.Empty(t, responseIDs)
}

func TestReadResponsesSSEDiscardsPartialFrameAtEOF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		stream         string
		wantOutput     string
		wantIdentities int
	}{
		{
			name:   "unterminated frame",
			stream: `data: {"type":"response.output_text.delta","delta":"partial","sequence_number":1}` + "\n",
		},
		{
			name:       "blank-line-delimited text followed by EOF",
			stream:     `data: {"type":"response.output_text.delta","delta":"complete","sequence_number":1}` + "\n\n",
			wantOutput: "[agent] complete\n",
		},
		{
			name: "delimited text then unterminated frame",
			stream: `data: {"type":"response.output_text.delta","delta":"complete","sequence_number":1}` + "\n\n" +
				`data: {"type":"response.output_text.delta","delta":"partial","sequence_number":2}` + "\n",
			wantOutput: "[agent] complete\n",
		},
		{
			name:           "delimited event without text",
			stream:         `data: {"type":"response.in_progress","response":{"id":"resp_123"}}` + "\n\n",
			wantIdentities: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			var responseIDs []string
			err := readResponsesSSEForTest(t.Context(), strings.NewReader(tt.stream), &output, "agent", false,
				func(responseID string) error {
					responseIDs = append(responseIDs, responseID)
					return nil
				})

			require.NoError(t, err)
			assert.Equal(t, tt.wantOutput, output.String())
			assert.Len(t, responseIDs, tt.wantIdentities)
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
	var responseIDs []string
	err := readResponsesSSEForTest(t.Context(), strings.NewReader(stream), &output, "agent", true,
		func(responseID string) error {
			responseIDs = append(responseIDs, responseID)
			return nil
		})

	require.EqualError(t, err, "agent failed (runtime_error): agent crashed")
	assert.Empty(t, output.String())
	assert.Equal(t, []string{"resp_123"}, responseIDs)
}

func TestReadResponsesSSECancelledOutcome(t *testing.T) {
	t.Parallel()

	stream := "event: response.output_text.delta\n" +
		`data: {"response":{"id":"resp_123","status":"in_progress"},"delta":"partial","sequence_number":1}` +
		"\n\n" +
		"event: response.cancelled\n" +
		`data: {"response":{"id":"resp_123","status":"cancelled"},"sequence_number":2}` + "\n\n"

	for _, tt := range []struct {
		name            string
		requireTerminal bool
		wantErr         string
	}{
		{name: "attached background returns error", requireTerminal: true, wantErr: "this invocation was cancelled"},
		{name: "foreground compatibility", requireTerminal: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			var responseIDs []string
			err := readResponsesSSEForTest(
				t.Context(),
				strings.NewReader(stream),
				&output,
				"agent",
				tt.requireTerminal,
				func(responseID string) error {
					responseIDs = append(responseIDs, responseID)
					return nil
				},
			)

			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tt.wantErr)
			}
			assert.Equal(t, "[agent] partial\n", output.String())
			assert.Equal(t, []string{"resp_123"}, responseIDs)
		})
	}
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

	var responseIDs []string
	var output bytes.Buffer
	err := readResponsesSSEForTest(t.Context(), reader, &output, "agent", true,
		func(responseID string) error {
			responseIDs = append(responseIDs, responseID)
			return nil
		})

	require.NoError(t, err)
	assert.Equal(t, []string{"resp_123"}, responseIDs)
	assert.Equal(t, "[agent] done\n", output.String())
}

func TestReadResponsesSSETerminalUsesExpectedIdentity(t *testing.T) {
	t.Parallel()

	stream := "event: response.completed\n" +
		`data: {"response":{"status":"completed"},"sequence_number":2}` + "\n\n"

	err := readResponsesSSEWithExpectedIDForTest(
		t.Context(),
		strings.NewReader(stream),
		io.Discard,
		"agent",
		true,
		"resp_123",
		nil,
	)

	require.NoError(t, err)
}

func TestReadResponsesSSEIgnoresSequenceNumbers(t *testing.T) {
	t.Parallel()

	stream := "event: response.output_text.delta\n" +
		`data: {"delta":"one","sequence_number":1}` + "\n\n" +
		"event: response.output_text.delta\n" +
		`data: {"delta":"duplicate","sequence_number":1}` + "\n\n" +
		"event: response.completed\n" +
		`data: {"response":{"id":"resp_1","status":"completed"},"sequence_number":2}` + "\n\n"

	var output bytes.Buffer
	err := readResponsesSSEForTest(t.Context(), strings.NewReader(stream), &output, "agent", false, nil)
	require.NoError(t, err)
	assert.Equal(t, "[agent] oneduplicate\n", output.String())
}

func TestReadResponsesSSEValidatesExpectedIdentity(t *testing.T) {
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
			err := readResponsesSSEWithExpectedIDForTest(
				t.Context(),
				strings.NewReader(stream),
				io.Discard,
				"agent",
				false,
				"resp_123",
				nil,
			)

			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestReadResponsesSSEEventSizeLimit(t *testing.T) {
	t.Parallel()

	const limit = maxResponsesSSEEventBytes
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
			stream: "event: ignored\n" + strings.Repeat("data:\n", limit+1) + "\n",
		},
		{
			name:    "many empty data fields exceed limit",
			stream:  "event: ignored\n" + strings.Repeat("data:\n", limit+2) + "\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := readResponsesSSE(
				t.Context(), strings.NewReader(tt.stream), io.Discard, "agent", responsesSSEOptions{},
			)
			if tt.wantErr {
				require.EqualError(t, err, "Responses SSE event exceeds 4194304 bytes")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
