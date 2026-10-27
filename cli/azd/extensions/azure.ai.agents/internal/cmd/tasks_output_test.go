// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestTasksJSONPreservesServiceResponse(t *testing.T) {
	for _, operation := range []string{"list", "show"} {
		t.Run(operation, func(t *testing.T) {
			api := &tasksAPIMock{}
			raw := json.RawMessage(`{"id":"task","unknown":{"integer":90071992547409931234567890,` +
				`"decimal":1.2345678901234567890123456789,"status":"future_status"},` +
				`"last_id":"opaque&<cursor>","data":[{"attachments":{"key":{"secret":"JSON & <> full detail"}}}]}`)
			if operation == "list" {
				api.On("ListTasks", mock.Anything, mock.Anything).Return(raw, nil).Once()
			} else {
				api.On("GetTask", mock.Anything, "id").Return(raw, nil).Once()
			}
			var writer bytes.Buffer
			action := &tasksAction{api: api, flags: &tasksFlags{}, target: &AgentContext{Name: "worker"}, writer: &writer}
			require.NoError(t, action.run(t.Context(), operation, []string{"id"}))
			var compact bytes.Buffer
			require.NoError(t, json.Compact(&compact, writer.Bytes()))
			require.Equal(t, string(raw), compact.String())
			require.Contains(t, writer.String(), "90071992547409931234567890")
			api.AssertExpectations(t)
		})
	}
}

func TestTasksTableResponsiveAndSafe(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	raw := json.RawMessage(`{"data":[{"id":"task\u001b[31m\nFAKE","status":"future_status",` +
		`"agent_name":"worker","session_id":"session","updated_at":1700000000,` +
		`"payload":"payload-secret","error":{"message":"error-secret"},` +
		`"attachments":[{"value":"attachment-secret"}],"unknown":"unknown-secret"}],` +
		`"has_more":true,"last_id":"cursor-secret"}`)
	for _, width := range []int{120, 80, 35} {
		t.Run(strings.Repeat("w", width), func(t *testing.T) {
			var writer bytes.Buffer
			formatter := &output.PrettyTableFormatter{ConsoleWidthFn: func() int { return width }}
			require.NoError(t, writeTasksTableWithFormatter(&writer, raw, true, formatter))
			text := writer.String()
			require.Contains(t, text, "task")
			require.Contains(t, text, "future_status")
			require.NotContains(t, text, "\x1b")
			require.NotContains(t, text, "\nFAKE")
			for _, secret := range []string{
				"payload-secret", "error-secret", "attachment-secret", "unknown-secret", "cursor-secret",
			} {
				require.NotContains(t, text, secret)
			}
			require.Contains(t, text, "--output json")
			require.Contains(t, text, "last_id")
			require.Contains(t, text, "same filters, --order, and --limit")
			if width == 35 {
				require.Contains(t, text, "STATUS:")
			}
		})
	}
}

func TestTasksShowTableSafeDetail(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	raw := json.RawMessage(`{"id":"task","status":"in_progress","agent_name":"worker","session_id":"session",` +
		`"created_at":1700000000,"updated_at":"2024-01-02T03:04:05Z","completed_at":null,` +
		`"lease":{"owner":"owner\u001b[2J","expires_at":"2024-01-02T03:05:05Z"},` +
		`"started_at":"2024-01-02T03:03:05Z",` +
		`"payload":{"secret":"payload-secret"},"error":{"message":"error-secret"},` +
		`"attachments":[{"value":"attachment-secret"}]}`)
	var writer bytes.Buffer
	require.NoError(t, writeTasksTableWithFormatter(&writer, raw, false,
		&output.PrettyTableFormatter{ConsoleWidthFn: func() int { return 35 }}))
	for _, expected := range []string{
		"task", "in_progress", "worker", "session", "2024-01-02T03:04:05Z",
		"Created:", "2023-11-14T22:13:20Z", "Started: 2024-01-02T03:03:05Z",
		"Lease owner: owner", "Lease expiry: 2024-01-02T03:05:05Z", "Has error: true",
	} {
		require.Contains(t, writer.String(), expected)
	}
	for _, secret := range []string{"payload-secret", "error-secret", "attachment-secret", "\x1b"} {
		require.NotContains(t, writer.String(), secret)
	}
}

func TestTasksTableEmptyMissingAndInvalid(t *testing.T) {
	for _, raw := range []string{`{"data":[]}`, `{"data":null}`, `{}`} {
		var writer bytes.Buffer
		require.NoError(t, writeTasksTable(&writer, json.RawMessage(raw), true))
		require.Equal(t, "No Tasks on this page.\n", writer.String())
	}
	var writer bytes.Buffer
	require.NoError(t, writeTasksTable(&writer, json.RawMessage(`{"data":[],"has_more":true}`), true))
	require.Contains(t, writer.String(), "last_id")
	writer.Reset()
	require.NoError(t, writeTasksTableWithFormatter(&writer, json.RawMessage(`{"data":[{}]}`), true,
		&output.PrettyTableFormatter{ConsoleWidthFn: func() int { return 120 }}))
	require.Contains(t, writer.String(), "-")
	for _, raw := range []string{`{`, `{"data":"secret"}`, `{"data":[{"id":{}}]}`} {
		err := writeTasksTable(io.Discard, json.RawMessage(raw), true)
		require.ErrorContains(t, err, "invalid Tasks table response")
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestTaskDisplayTextBounded(t *testing.T) {
	text := taskDisplayText(strings.Repeat("a", 10000))
	require.LessOrEqual(t, len(text), 267)
	require.True(t, strings.HasSuffix(text, "..."))
	for _, value := range []string{"\x1b[2J\n\r\t", "\u009b31m", "a\u202eb", "a\u2028b"} {
		text := taskDisplayText(value)
		require.NotContains(t, text, "\x1b")
		require.NotContains(t, text, "\n")
		require.NotContains(t, text, "\r")
		require.NotContains(t, text, "\u009b")
		require.NotContains(t, text, "\u202e")
	}
}

type tasksFailingWriter struct{ err error }

func (w tasksFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestTasksOutputWriterErrors(t *testing.T) {
	failure := errors.New("writer failed")
	for _, tt := range []struct {
		raw  string
		list bool
	}{
		{`{"data":[]}`, true},
		{`{"data":[{"id":"task"}]}`, true},
		{`{"id":"task","has_error":true}`, false},
	} {
		require.ErrorIs(t, writeTasksTable(tasksFailingWriter{failure}, json.RawMessage(tt.raw), tt.list), failure)
	}
}
