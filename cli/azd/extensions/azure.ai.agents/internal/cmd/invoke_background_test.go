// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokeCommandBackgroundFlagRegistered(t *testing.T) {
	t.Parallel()

	flag := newInvokeCommand(nil).Flags().Lookup("background")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

type orderingResponseStore struct {
	saved   bool
	saveErr error
	saves   []savedBackgroundResponse
}

func (s *orderingResponseStore) Get(context.Context, string) (*savedBackgroundResponse, error) {
	return nil, nil
}

func (s *orderingResponseStore) Save(_ context.Context, _ string, record savedBackgroundResponse) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = true
	s.saves = append(s.saves, record)
	return nil
}

func (s *orderingResponseStore) Delete(context.Context, string) error {
	return nil
}

type afterSaveWriter struct {
	store  *orderingResponseStore
	output strings.Builder
}

func (w *afterSaveWriter) Write(p []byte) (int, error) {
	if !w.store.saved {
		return 0, errors.New("response ID printed before state was saved")
	}
	return w.output.Write(p)
}

func newTestProgressPersister(
	store *orderingResponseStore,
	writer io.Writer,
	now func() time.Time,
) *backgroundProgressPersister {
	persister := newBackgroundProgressPersister(store, "agent-key", "sess_123", "conv_123", writer)
	persister.now = now
	return persister
}

func TestBackgroundProgressPersisterSavesBeforePrinting(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	writer := &afterSaveWriter{store: store}
	now := time.Unix(1_000, 0)
	persister := newTestProgressPersister(store, writer, func() time.Time { return now })
	err := persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123",
		Cursor:     new(int64(0)),
		Status:     "in_progress",
		EventType:  "response.created",
	})

	require.NoError(t, err)
	require.Len(t, store.saves, 1)
	assert.Equal(t, "resp_123", store.saves[0].ResponseID)
	assert.Equal(t, "Response:     resp_123\n", writer.output.String())
}

func TestBackgroundProgressPersisterDoesNotPrintWhenSaveFails(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{saveErr: errors.New("write failed")}
	writer := &afterSaveWriter{store: store}
	persister := newTestProgressPersister(store, writer, time.Now)
	err := persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123",
		Cursor:     new(int64(0)),
		Status:     "in_progress",
		EventType:  "response.created",
	})

	require.EqualError(t, err, "write failed")
	assert.Empty(t, writer.output.String())
}

func TestBackgroundProgressPersisterThrottlesOrdinaryEvents(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	writer := &afterSaveWriter{store: store}
	now := time.Unix(1_000, 0)
	persister := newTestProgressPersister(store, writer, func() time.Time { return now })
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123",
		Cursor:     new(int64(0)),
		Status:     "in_progress",
		EventType:  "response.created",
	}))

	for sequenceNumber := int64(1); sequenceNumber < backgroundCursorPersistEventCount; sequenceNumber++ {
		require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
			ResponseID: "resp_123",
			Cursor:     new(sequenceNumber),
			Status:     "in_progress",
			EventType:  "response.output_text.delta",
		}))
	}
	assert.Len(t, store.saves, 1)

	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123",
		Cursor:     new(int64(backgroundCursorPersistEventCount)),
		Status:     "in_progress",
		EventType:  "response.output_text.delta",
	}))
	require.Len(t, store.saves, 2)
	assert.Equal(t, int64(backgroundCursorPersistEventCount), *store.saves[1].LastSequenceNumber)
}

func TestBackgroundProgressPersisterPersistsAfterInterval(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	writer := &afterSaveWriter{store: store}
	now := time.Unix(1_000, 0)
	persister := newTestProgressPersister(store, writer, func() time.Time { return now })
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(0)), Status: "in_progress", EventType: "response.created",
	}))

	now = now.Add(backgroundCursorPersistInterval - time.Millisecond)
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(1)), Status: "in_progress", EventType: "response.output_text.delta",
	}))
	assert.Len(t, store.saves, 1)

	now = now.Add(time.Millisecond)
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(2)), Status: "in_progress", EventType: "response.output_text.delta",
	}))
	assert.Len(t, store.saves, 2)
}

func TestBackgroundProgressPersisterPersistsLifecycleAndTerminalEvents(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	writer := &afterSaveWriter{store: store}
	now := time.Unix(1_000, 0)
	persister := newTestProgressPersister(store, writer, func() time.Time { return now })
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(0)), Status: "in_progress", EventType: "response.created",
	}))
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(1)), Status: "in_progress", EventType: "response.in_progress",
	}))
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(2)), Status: "completed", EventType: "response.completed", Terminal: true,
	}))

	require.Len(t, store.saves, 3)
	assert.Equal(t, int64(0), *store.saves[0].LastSequenceNumber)
	assert.Equal(t, int64(1), *store.saves[1].LastSequenceNumber)
	assert.Equal(t, int64(2), *store.saves[2].LastSequenceNumber)
	assert.Equal(t, "completed", store.saves[2].Status)
}

func TestBackgroundProgressPersisterFlushesPendingCursor(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	writer := &afterSaveWriter{store: store}
	now := time.Unix(1_000, 0)
	persister := newTestProgressPersister(store, writer, func() time.Time { return now })
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(0)), Status: "in_progress", EventType: "response.created",
	}))
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(1)), Status: "in_progress", EventType: "response.output_text.delta",
	}))
	assert.Len(t, store.saves, 1)

	require.NoError(t, persister.Flush(t.Context()))
	require.Len(t, store.saves, 2)
	assert.Equal(t, int64(1), *store.saves[1].LastSequenceNumber)
}

func TestInvokeCommandBackgroundValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		want     string
		wantCode string
	}{
		{
			name: "rejects local",
			args: []string{"--background", "--local", "hello"},
			want: "supported only for remote Responses agents",
		},
		{
			name: "rejects explicit invocations protocol",
			args: []string{"--background", "--protocol", "invocations", "hello"},
			want: "--background is not supported with the invocations protocol",
		},
		{
			name:     "rejects explicit timeout",
			args:     []string{"--background", "--timeout", "1", "hello"},
			want:     "--timeout cannot be used with --background",
			wantCode: exterrors.CodeConflictingArguments,
		},
		{
			name:     "rejects explicitly set default timeout",
			args:     []string{"--background", "--timeout", "1800", "hello"},
			want:     "--timeout cannot be used with --background",
			wantCode: exterrors.CodeConflictingArguments,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := newInvokeCommand(nil)
			cmd.SetArgs(tt.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), tt.want), "error %q should contain %q", err, tt.want)
			if tt.wantCode != "" {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, tt.wantCode, localErr.Code)
			}
		})
	}
}

func TestInvokeCommandBackgroundEndpointRequiresPersistentStateAtRuntime(t *testing.T) {
	isolateFromAzdDaemon(t)

	cmd := newInvokeCommand(nil)
	cmd.SetArgs([]string{
		"--background",
		"--agent-endpoint",
		"https://acct.services.ai.azure.com/api/projects/proj/agents/test-agent/endpoint/protocols/openai/responses",
		"hello",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "background Responses require persistent azd state")
	assert.NotContains(t, err.Error(), "--background is not supported with --agent-endpoint")
}

type capturedBackgroundRequest struct {
	method string
	path   string
	query  string
	header http.Header
	body   struct {
		Input          string `json:"input"`
		Stream         bool   `json:"stream"`
		Store          bool   `json:"store"`
		Background     bool   `json:"background"`
		AgentSessionID string `json:"agent_session_id"`
		SessionID      string `json:"session_id"`
		Conversation   struct {
			ID string `json:"id"`
		} `json:"conversation"`
	}
	err error
}

func TestResponsesRemoteBackground(t *testing.T) {
	tests := []struct {
		name            string
		selectedSession string
		assignedSession string
		streamDelay     time.Duration
	}{
		{
			name:            "selected session and no overall timeout",
			selectedSession: "selected-session",
			streamDelay:     1100 * time.Millisecond,
		},
		{
			name:            "endpoint with state and server-assigned session",
			assignedSession: "assigned-session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan capturedBackgroundRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured := capturedBackgroundRequest{
					method: r.Method,
					path:   r.URL.Path,
					query:  r.URL.RawQuery,
					header: r.Header.Clone(),
				}
				captured.err = json.NewDecoder(r.Body).Decode(&captured.body)
				requests <- captured

				w.Header().Set("Content-Type", "text/event-stream")
				if tt.assignedSession != "" {
					w.Header().Set("x-agent-session-id", tt.assignedSession)
				}
				w.WriteHeader(http.StatusOK)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				time.Sleep(tt.streamDelay)
				_, _ = io.WriteString(w,
					"event: response.created\n"+
						`data: {"type":"response.created","response":{"id":"resp_123","status":"in_progress"},"sequence_number":0}`+
						"\n\n"+
						"event: response.completed\n"+
						`data: {"type":"response.completed","response":{"id":"resp_123","status":"completed"},"sequence_number":1}`+
						"\n\n",
				)
			}))
			t.Cleanup(server.Close)

			userConfig := newInvokeUserConfigServer()
			azdClient := newInvokeTestAzdClient(t, userConfig)
			agentKey := "agent-key"
			var output bytes.Buffer
			var endpoint *parsedAgentEndpoint
			if tt.assignedSession != "" {
				endpoint = &parsedAgentEndpoint{
					ProjectEndpoint: server.URL + "/api/projects/test-project",
					AgentName:       "test-agent",
				}
				agentKey = buildAgentKey(endpoint.ProjectEndpoint, endpoint.AgentName, "", false)
			}
			action := &InvokeAction{
				flags: &invokeFlags{
					message:      "long task",
					timeout:      1,
					session:      tt.selectedSession,
					conversation: "conv_123",
					background:   true,
					outputFmt:    outputDefault,
				},
				writer:   &output,
				endpoint: endpoint,
				resolveRemoteContextFn: func(context.Context) (*remoteContext, error) {
					return &remoteContext{
						name:            "test-agent",
						agentKey:        agentKey,
						projectEndpoint: server.URL + "/api/projects/test-project",
						apiVersion:      "v1",
						azdClient:       azdClient,
					}, nil
				},
				acquireBearerTokenFn: func(context.Context) (string, error) {
					return "test-token", nil
				},
			}

			// The delayed stream exceeds the configured foreground timeout, proving
			// the background client has no total request deadline.
			started := time.Now()
			require.NoError(t, action.responsesRemote(t.Context()))
			if tt.streamDelay > 0 {
				assert.GreaterOrEqual(t, time.Since(started), tt.streamDelay)
			}

			captured := <-requests
			require.NoError(t, captured.err)
			assert.Equal(t, http.MethodPost, captured.method)
			assert.Equal(t,
				"/api/projects/test-project/agents/test-agent/endpoint/protocols/openai/responses",
				captured.path,
			)
			assert.Equal(t, "api-version=v1", captured.query)
			assert.Equal(t, "Bearer test-token", captured.header.Get("Authorization"))
			assert.Equal(t, "long task", captured.body.Input)
			assert.True(t, captured.body.Stream)
			assert.True(t, captured.body.Store)
			assert.True(t, captured.body.Background)
			assert.Equal(t, tt.selectedSession, captured.body.AgentSessionID)
			assert.Empty(t, captured.body.SessionID)
			assert.Equal(t, "conv_123", captured.body.Conversation.ID)

			expectedSession := tt.selectedSession
			if expectedSession == "" {
				expectedSession = tt.assignedSession
			}
			var sessions map[string]string
			userConfig.getJSON(t, configPath("sessions"), &sessions)
			assert.Equal(t, expectedSession, sessions[agentKey])

			var responses map[string]savedBackgroundResponse
			userConfig.getJSON(t, backgroundResponsesConfigPath, &responses)
			saved := responses[agentKey]
			assert.Equal(t, "resp_123", saved.ResponseID)
			require.NotNil(t, saved.LastSequenceNumber)
			assert.Equal(t, int64(1), *saved.LastSequenceNumber)
			assert.Equal(t, "completed", saved.Status)
			assert.Equal(t, expectedSession, saved.SessionID)
			assert.Equal(t, "conv_123", saved.ConversationID)
			assert.Contains(t, output.String(), "Response:     resp_123")
		})
	}
}

func TestResponsesRemoteBackgroundEndpointWithoutPersistentState(t *testing.T) {
	action := &InvokeAction{
		flags: &invokeFlags{
			message:    "long task",
			background: true,
			outputFmt:  outputDefault,
		},
		endpoint: &parsedAgentEndpoint{
			ProjectEndpoint: "https://acct.services.ai.azure.com/api/projects/proj",
			AgentName:       "test-agent",
		},
		resolveRemoteContextFn: func(context.Context) (*remoteContext, error) {
			return &remoteContext{
				name:            "test-agent",
				agentKey:        "stable-agent-key",
				projectEndpoint: "https://acct.services.ai.azure.com/api/projects/proj",
			}, nil
		},
	}

	err := action.responsesRemote(t.Context())
	require.EqualError(t, err,
		"background Responses require persistent azd state; "+
			"run through azd instead of the extension executable directly")
}
