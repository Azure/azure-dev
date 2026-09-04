// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInvokeCommandLifecycleFlagsRegistered(t *testing.T) {
	t.Parallel()

	flags := newInvokeCommand(nil).Flags()
	for _, name := range []string{"resumable", "resume", "steer", "cancel"} {
		flag := flags.Lookup(name)
		require.NotNil(t, flag)
		assert.Equal(t, "false", flag.DefValue)
	}
	assert.Nil(t, flags.Lookup("agent-name"))
	assert.Nil(t, flags.Lookup("background"))
	assert.Nil(t, flags.Lookup("continue"))
}

type orderingResponseStore struct {
	saved        bool
	saveErr      error
	saves        []savedBackgroundResponse
	saveContexts []context.Context
	record       savedBackgroundResponse
	getRecord    *savedBackgroundResponse
}

func (s *orderingResponseStore) Get(context.Context, string) (*savedBackgroundResponse, error) {
	return s.getRecord, nil
}

func (s *orderingResponseStore) Save(ctx context.Context, _ string, record savedBackgroundResponse) error {
	s.saveContexts = append(s.saveContexts, ctx)
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = true
	s.saves = append(s.saves, record)
	s.record = record
	return nil
}

func (s *orderingResponseStore) Delete(context.Context, string) error {
	return nil
}

type afterSaveWriter struct {
	store  *orderingResponseStore
	output strings.Builder
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func (w *afterSaveWriter) Write(p []byte) (int, error) {
	if !w.store.saved {
		return 0, errors.New("response ID printed before state was saved")
	}
	return w.output.Write(p)
}

type inertPersistTimer struct{}

func (inertPersistTimer) Stop() bool {
	return true
}

type manualPersistTimer struct {
	callback func()
	stopped  bool
	fired    bool
}

type manualPersistTimerCapture struct {
	timer *manualPersistTimer
	delay time.Duration
}

func (t *manualPersistTimer) Stop() bool {
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

func (t *manualPersistTimer) Fire() {
	if t.stopped || t.fired {
		return
	}
	t.fired = true
	t.callback()
}

func newTestProgressPersister(
	store *orderingResponseStore,
	writer io.Writer,
	now func() time.Time,
) *backgroundProgressPersister {
	persister := newBackgroundProgressPersister(store, "agent-key", "sess_123", "conv_123", writer)
	persister.now = now
	persister.timerFactory = func(time.Duration, func()) backgroundPersistTimer {
		return inertPersistTimer{}
	}
	return persister
}

func useManualPersistTimer(t *testing.T, persister *backgroundProgressPersister) *manualPersistTimerCapture {
	t.Helper()

	capture := &manualPersistTimerCapture{}
	persister.timerFactory = func(delay time.Duration, callback func()) backgroundPersistTimer {
		capture.delay = delay
		capture.timer = &manualPersistTimer{callback: callback}
		return capture.timer
	}
	return capture
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

func TestBackgroundProgressPersisterPrintsRecoveryIDWhenFirstSaveFails(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{saveErr: errors.New("write failed")}
	var output bytes.Buffer
	persister := newTestProgressPersister(store, &output, time.Now)
	err := persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123",
		Cursor:     new(int64(0)),
		Status:     "in_progress",
		EventType:  "response.created",
	})

	require.EqualError(t, err, "write failed")
	assert.Contains(t, output.String(), "Response:     resp_123")
	assert.Contains(t, output.String(), "state was not saved")
	assert.Contains(t, output.String(), "Save the Response ID before retrying")
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

func TestBackgroundProgressPersisterTimerFlushesQuietStreamOnce(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	now := time.Unix(1_000, 0)
	persister := newTestProgressPersister(store, io.Discard, func() time.Time { return now })
	timer := useManualPersistTimer(t, persister)
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(0)), Status: "in_progress", EventType: "response.created",
	}))
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(1)), Status: "in_progress", EventType: "response.output_text.delta",
	}))
	require.Len(t, store.saves, 1)
	require.NotNil(t, timer.timer)
	assert.Equal(t, backgroundCursorPersistInterval, timer.delay)

	timer.timer.Fire()

	require.Len(t, store.saves, 2)
	assert.Equal(t, int64(1), *store.saves[1].LastSequenceNumber)
	require.Len(t, store.saveContexts, 2)
	deadline, ok := store.saveContexts[1].Deadline()
	require.True(t, ok)
	remaining := time.Until(deadline)
	assert.Positive(t, remaining)
	assert.LessOrEqual(t, remaining, backgroundCursorPersistTimeout)
	assert.ErrorIs(t, store.saveContexts[1].Err(), context.Canceled)
	require.NoError(t, persister.Flush(t.Context()))
	assert.Len(t, store.saves, 2)
	require.NoError(t, persister.Close())
}

func TestBackgroundProgressPersisterTimerUsesRemainingInterval(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	now := time.Unix(1_000, 0)
	persister := newTestProgressPersister(store, io.Discard, func() time.Time { return now })
	timer := useManualPersistTimer(t, persister)
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(0)), Status: "in_progress", EventType: "response.created",
	}))

	now = now.Add(backgroundCursorPersistInterval - 100*time.Millisecond)
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(1)), Status: "in_progress", EventType: "response.output_text.delta",
	}))

	require.NotNil(t, timer.timer)
	assert.Equal(t, 100*time.Millisecond, timer.delay)
	require.NoError(t, persister.Close())
}

func TestBackgroundProgressPersisterPersistsImmediatelyAtInterval(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	now := time.Unix(1_000, 0)
	persister := newTestProgressPersister(store, io.Discard, func() time.Time { return now })
	timer := useManualPersistTimer(t, persister)
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(0)), Status: "in_progress", EventType: "response.created",
	}))

	now = now.Add(backgroundCursorPersistInterval)
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(1)), Status: "in_progress", EventType: "response.output_text.delta",
	}))

	assert.Nil(t, timer.timer)
	require.Len(t, store.saves, 2)
	assert.Equal(t, int64(1), *store.saves[1].LastSequenceNumber)
	require.NoError(t, persister.Close())
}

func TestBackgroundProgressPersisterCloseStopsTimerAfterCancellation(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	persister := newTestProgressPersister(store, io.Discard, time.Now)
	timer := useManualPersistTimer(t, persister)
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(0)), Status: "in_progress", EventType: "response.created",
	}))
	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, persister.Apply(ctx, responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(1)), Status: "in_progress", EventType: "response.output_text.delta",
	}))
	cancel()

	require.NoError(t, persister.Close())
	timer.timer.Fire()
	assert.Len(t, store.saves, 1)
	require.ErrorIs(t, persister.Apply(t.Context(), responsesStreamProgress{}), errBackgroundProgressPersisterClosed)
}

func TestBackgroundProgressPersisterSurfacesTimerError(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	persister := newTestProgressPersister(store, io.Discard, time.Now)
	timer := useManualPersistTimer(t, persister)
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(0)), Status: "in_progress", EventType: "response.created",
	}))
	require.NoError(t, persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(1)), Status: "in_progress", EventType: "response.output_text.delta",
	}))
	store.saveErr = errors.New("timed write failed")

	timer.timer.Fire()

	store.saveErr = nil
	err := persister.Apply(t.Context(), responsesStreamProgress{
		ResponseID: "resp_123", Cursor: new(int64(2)), Status: "in_progress", EventType: "response.output_text.delta",
	})
	require.EqualError(t, err, "timed write failed")
	require.NoError(t, persister.Flush(t.Context()))
	require.Len(t, store.saves, 2)
	assert.Equal(t, int64(1), *store.saves[1].LastSequenceNumber)
	require.NoError(t, persister.Close())
}

func TestHandleRejectedResponseCancelTreatsTerminalSnapshotAsSuccess(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"completed", "failed", "incomplete", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, err := fmt.Fprintf(w, `{"id":"resp_123","status":%q}`, status)
				assert.NoError(t, err)
			}))
			defer server.Close()

			store := &orderingResponseStore{}
			rc := &remoteContext{
				name:            "agent",
				agentKey:        "agent-key",
				projectEndpoint: server.URL,
				apiVersion:      "2026-05-01",
				bearerToken:     "test-token",
			}
			var output strings.Builder
			cancelErr := errors.New("cancel rejected")
			err := (&InvokeAction{flags: &invokeFlags{}}).handleRejectedResponseCancel(
				t.Context(),
				rc,
				store,
				savedBackgroundResponse{ResponseID: "resp_123", Status: "in_progress"},
				cancelErr,
				&output,
			)

			require.NoError(t, err)
			require.Len(t, store.saves, 1)
			assert.Equal(t, status, store.saves[0].Status)
			assert.Equal(t, "Response resp_123 is already "+status+"; nothing to cancel.\n", output.String())
		})
	}
}

func TestHandleRejectedResponseCancelPreservesOriginalError(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		statusCode int
		body       string
		wantSaves  int
	}{
		{
			name:       "snapshot remains active",
			statusCode: http.StatusOK,
			body:       `{"id":"resp_123","status":"in_progress"}`,
			wantSaves:  1,
		},
		{
			name:       "snapshot retrieval fails",
			statusCode: http.StatusInternalServerError,
			body:       `{"error":{"message":"snapshot unavailable"}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, err := io.WriteString(w, tt.body)
				assert.NoError(t, err)
			}))
			defer server.Close()

			store := &orderingResponseStore{}
			rc := &remoteContext{
				name:            "agent",
				agentKey:        "agent-key",
				projectEndpoint: server.URL,
				apiVersion:      "2026-05-01",
				bearerToken:     "test-token",
			}
			var output strings.Builder
			cancelErr := errors.New("cancel rejected")
			err := (&InvokeAction{flags: &invokeFlags{}}).handleRejectedResponseCancel(
				t.Context(),
				rc,
				store,
				savedBackgroundResponse{ResponseID: "resp_123", Status: "in_progress"},
				cancelErr,
				&output,
			)

			require.ErrorIs(t, err, cancelErr)
			assert.Len(t, store.saves, tt.wantSaves)
			assert.Empty(t, output.String())
		})
	}
}

func TestPrintResponseStatus(t *testing.T) {
	t.Parallel()

	var output strings.Builder
	require.NoError(t, printResponseStatus(&output, "completed"))
	assert.Equal(t, "Status: completed\n", output.String())
}

func TestPrintResponseStatusPropagatesWriterError(t *testing.T) {
	t.Parallel()

	writeErr := errors.New("write failed")
	require.ErrorIs(t, printResponseStatus(failingWriter{err: writeErr}, "completed"), writeErr)
}

func TestFollowBackgroundResponseWithSavedTerminalStatusDoesNotFetch(t *testing.T) {
	t.Parallel()

	var output strings.Builder
	err := (&InvokeAction{}).followBackgroundResponse(
		t.Context(),
		nil,
		nil,
		savedBackgroundResponse{ResponseID: "resp_123", Status: "completed"},
		&output,
	)

	require.NoError(t, err)
	assert.Equal(t, "Status: completed\n", output.String())
}

func TestHandleEmptyBackgroundFollow(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		status     string
		wantOutput string
		wantErr    string
	}{
		{
			name:       "terminal snapshot renders output and status",
			status:     "completed",
			wantOutput: "[agent] recovered output\nStatus: completed\n",
		},
		{
			name:    "active snapshot returns guidance without rendering",
			status:  "in_progress",
			wantErr: "no new stream events were available",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, err := fmt.Fprintf(
					w,
					`{"id":"resp_123","status":%q,"output":[{"content":[{"type":"output_text","text":"recovered output"}]}]}`,
					tt.status,
				)
				assert.NoError(t, err)
			}))
			defer server.Close()

			store := &orderingResponseStore{}
			rc := &remoteContext{
				name:            "agent",
				agentKey:        "agent-key",
				projectEndpoint: server.URL,
				apiVersion:      "2026-05-01",
				bearerToken:     "test-token",
			}
			var output strings.Builder
			attempt, err := (&InvokeAction{flags: &invokeFlags{}}).handleEmptyBackgroundFollow(
				t.Context(),
				rc,
				store,
				savedBackgroundResponse{ResponseID: "resp_123", Status: "in_progress"},
				&output,
			)

			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.True(t, attempt.completed)
				assert.Equal(t, tt.status, attempt.record.Status)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
				assert.False(t, attempt.completed)
			}
			assert.Equal(t, tt.wantOutput, output.String())
			require.Len(t, store.saves, 1)
			assert.Equal(t, tt.status, store.saves[0].Status)
		})
	}
}

func TestPrepareResponseStateForNewTurnRequiresAzdState(t *testing.T) {
	t.Parallel()

	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprintf("background=%t", background), func(t *testing.T) {
			t.Parallel()
			action := &InvokeAction{flags: &invokeFlags{resumable: background}}
			store, err := action.prepareResponseStateForNewTurn(t.Context(), &remoteContext{
				agentKey: "endpoint-derived-key",
			})

			assert.Nil(t, store)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeResponseStateUnavailable, localErr.Code)
			require.ErrorContains(t, err, "remote Responses require access to azd state")
		})
	}
}

func TestEnsureNoActiveBackgroundResponseRefreshesStaleState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		initialStatus string
		freshStatus   string
		wantRefresh   bool
		wantErr       string
	}{
		{
			name:          "completed response allows new turn",
			initialStatus: "in_progress",
			freshStatus:   "completed",
			wantRefresh:   true,
		},
		{
			name:          "still active response blocks new turn",
			initialStatus: "queued",
			freshStatus:   "in_progress",
			wantRefresh:   true,
			wantErr:       "background Response resp_123 is still active",
		},
		{
			name:          "known terminal response skips refresh",
			initialStatus: "failed",
			freshStatus:   "completed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount.Add(1)
				assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				_, err := fmt.Fprintf(w, `{"id":"resp_123","status":%q}`, tt.freshStatus)
				assert.NoError(t, err)
			}))
			defer server.Close()

			record := &savedBackgroundResponse{ResponseID: "resp_123", Status: tt.initialStatus}
			store := &orderingResponseStore{getRecord: record}
			action := &InvokeAction{flags: &invokeFlags{}}
			rc := &remoteContext{
				name:            "agent",
				agentKey:        "agent-key",
				projectEndpoint: server.URL,
				apiVersion:      "2026-05-01",
				bearerToken:     "test-token",
			}
			err := action.ensureNoActiveBackgroundResponse(t.Context(), rc, store)

			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}
			if tt.wantRefresh {
				assert.Equal(t, int32(1), requestCount.Load())
				require.Len(t, store.saves, 1)
				assert.Equal(t, tt.freshStatus, store.saves[0].Status)
			} else {
				assert.Zero(t, requestCount.Load())
				assert.Empty(t, store.saves)
			}
		})
	}
}

func TestRefreshResponseSnapshotPersistsWithoutRendering(t *testing.T) {
	t.Parallel()

	var requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		_, err := io.WriteString(w, `{"id":"resp_123","status":"completed"}`)
		assert.NoError(t, err)
	}))
	defer server.Close()

	store := &orderingResponseStore{}
	record := savedBackgroundResponse{
		ResponseID:     "resp_123",
		Status:         "in_progress",
		SessionID:      "sess_123",
		ConversationID: "conv_123",
	}
	action := &InvokeAction{flags: &invokeFlags{}}
	rc := &remoteContext{
		name:            "agent",
		agentKey:        "agent-key",
		projectEndpoint: server.URL,
		apiVersion:      "2026-05-01",
	}

	updated, result, err := action.refreshResponseSnapshot(t.Context(), rc, store, record, "test-token")

	require.NoError(t, err)
	assert.Contains(t, requestPath, "/agents/agent/endpoint/protocols/openai/responses/resp_123")
	assert.Equal(t, "completed", result.snapshot.Status)
	assert.JSONEq(t, `{"id":"resp_123","status":"completed"}`, string(result.raw))
	assert.Equal(t, "in_progress", record.Status, "input record must not be mutated")
	assert.Equal(t, "completed", updated.Status)
	require.Len(t, store.saves, 1)
	assert.Equal(t, updated, store.saves[0])
}

func TestRefreshResponseSnapshotRejectsMismatchedIdentityWithoutPersisting(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := io.WriteString(w, `{"id":"resp_other","status":"completed"}`)
		assert.NoError(t, err)
	}))
	defer server.Close()

	store := &orderingResponseStore{}
	record := savedBackgroundResponse{ResponseID: "resp_123", Status: "in_progress"}
	rc := &remoteContext{
		name:            "agent",
		agentKey:        "agent-key",
		projectEndpoint: server.URL,
		apiVersion:      "2026-05-01",
	}
	updated, _, err := (&InvokeAction{flags: &invokeFlags{}}).refreshResponseSnapshot(
		t.Context(), rc, store, record, "test-token",
	)

	require.ErrorContains(t, err, `snapshot ID "resp_other" does not match saved ID "resp_123"`)
	assert.Equal(t, savedBackgroundResponse{}, updated)
	assert.Equal(t, "in_progress", record.Status)
	assert.Empty(t, store.saves)
}

func TestNextReconnectFailureCount(t *testing.T) {
	t.Parallel()

	failures := 0
	for attempt := range maxConsecutiveReconnectFailures {
		failures = nextReconnectFailureCount(failures, false)
		assert.Equal(t, attempt+1, failures)
		assert.Equal(t, attempt+1 >= maxConsecutiveReconnectFailures, failures >= maxConsecutiveReconnectFailures)
	}

	failures = nextReconnectFailureCount(maxConsecutiveReconnectFailures-1, true)
	assert.Equal(t, 1, failures)
	assert.Less(t, failures, maxConsecutiveReconnectFailures)
}

func TestReconnectRetryDelay(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 4*time.Second, reconnectRetryDelay(0, 2))
	assert.Equal(t, 5*time.Second, reconnectRetryDelay(5*time.Second, 2))
	assert.Equal(t, 30*time.Second, reconnectRetryDelay(time.Minute, 2))
}

func TestClassifyBackgroundFollowResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		status         int
		header         http.Header
		wantFatal      bool
		wantRetryAfter time.Duration
	}{
		{name: "success", status: http.StatusOK},
		{name: "non-retryable", status: http.StatusBadRequest, wantFatal: true},
		{
			name:           "retry after seconds",
			status:         http.StatusTooManyRequests,
			header:         http.Header{"Retry-After": []string{"5"}},
			wantRetryAfter: 5 * time.Second,
		},
		{
			name:           "retry after milliseconds",
			status:         http.StatusServiceUnavailable,
			header:         http.Header{"Retry-After-Ms": []string{"150"}},
			wantRetryAfter: 150 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{
				StatusCode: tt.status,
				Status:     http.StatusText(tt.status),
				Header:     tt.header,
				Body:       io.NopCloser(strings.NewReader("response body")),
			}
			result, err := classifyBackgroundFollowResponse(resp, "https://example.test/responses/resp_123")
			if tt.wantFatal {
				require.ErrorContains(t, err, "response body")
				return
			}

			require.NoError(t, err)
			if tt.status < 400 {
				assert.Same(t, resp, result.response)
				return
			}
			require.Error(t, result.retryCause)
			assert.Equal(t, tt.wantRetryAfter, result.retryAfter)
		})
	}
}

func TestClassifyResponseLifecycleHTTPError(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{
		exterrors.OpResumeBackgroundResponse,
		exterrors.OpSteerBackgroundResponse,
		exterrors.OpCancelBackgroundResponse,
	} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			cause := errors.Join(
				errors.New("operation failed"),
				&responseLifecycleHTTPError{
					method:     http.MethodGet,
					requestURL: "https://project.services.ai.azure.com/responses/resp_123",
					statusCode: http.StatusForbidden,
					status:     "403 Forbidden",
					body:       []byte(`{"error":{"message":"sensitive response"}}`),
				},
			)

			err := classifyResponseLifecycleHTTPError(cause, operation)

			serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
			require.True(t, ok)
			assert.Equal(t, operation+".403", serviceErr.ErrorCode)
			assert.Equal(t, http.StatusForbidden, serviceErr.StatusCode)
			assert.Equal(t, "project.services.ai.azure.com", serviceErr.ServiceName)
			assert.NotContains(t, serviceErr.Message, "sensitive response")
		})
	}
}

func TestClassifyResponseLifecycleHTTPErrorPreservesOtherErrors(t *testing.T) {
	t.Parallel()

	cause := errors.New("local failure")
	assert.Same(t, cause, classifyResponseLifecycleHTTPError(cause, exterrors.OpResumeBackgroundResponse))
}

func TestClassifyBackgroundFollowResponseUsesHTTPDate(t *testing.T) {
	t.Parallel()

	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Status:     http.StatusText(http.StatusServiceUnavailable),
		Header: http.Header{
			"Retry-After": []string{time.Now().Add(5 * time.Second).UTC().Format(time.RFC1123)},
		},
		Body: io.NopCloser(strings.NewReader("response body")),
	}
	result, err := classifyBackgroundFollowResponse(resp, "https://example.test/responses/resp_123")
	require.NoError(t, err)
	require.Error(t, result.retryCause)
	assert.GreaterOrEqual(t, result.retryAfter, 3*time.Second)
	assert.LessOrEqual(t, result.retryAfter, 5*time.Second)
}

func TestRetryableResponseStatus(t *testing.T) {
	t.Parallel()

	for status, want := range map[int]bool{
		http.StatusBadRequest:          false,
		http.StatusRequestTimeout:      true,
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusServiceUnavailable:  true,
	} {
		assert.Equal(t, want, isRetryableResponseStatus(status), "status %d", status)
	}
}

func TestSleepWithContextStopsOnCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	err := sleepWithContext(ctx, time.Hour)
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(started), time.Second)
}

func TestValidateInvokeOperationFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   invokeFlags
		changed map[string]string
		wantErr string
	}{
		{name: "ordinary invoke requires input", wantErr: "a message argument or --input-file is required"},
		{name: "ordinary invoke accepts message", flags: invokeFlags{message: "hello"}},
		{name: "ordinary invoke accepts file", flags: invokeFlags{inputFile: "request.json"}},
		{
			name:    "ordinary invoke rejects message and file",
			flags:   invokeFlags{message: "hello", inputFile: "request.json"},
			wantErr: "cannot use --input-file and a message argument together",
		},
		{
			name:    "background requires input",
			flags:   invokeFlags{resumable: true},
			wantErr: "a message argument or --input-file is required",
		},
		{name: "background accepts input", flags: invokeFlags{resumable: true, message: "hello"}},
		{
			name:  "background accepts no wait",
			flags: invokeFlags{resumable: true, noWait: true, message: "hello"},
		},
		{
			name:    "no wait requires background",
			flags:   invokeFlags{noWait: true, message: "hello"},
			wantErr: "--no-wait requires --resumable",
		},
		{name: "resume accepts empty input", flags: invokeFlags{resume: true}},
		{
			name:    "resume rejects message",
			flags:   invokeFlags{resume: true, message: "hello"},
			wantErr: "--resume and --cancel do not accept a message or --input-file",
		},
		{
			name:    "resume rejects file",
			flags:   invokeFlags{resume: true, inputFile: "request.json"},
			wantErr: "--resume and --cancel do not accept a message or --input-file",
		},
		{name: "steer accepts message", flags: invokeFlags{steer: true, message: "hello"}},
		{name: "steer accepts file", flags: invokeFlags{steer: true, inputFile: "request.json"}},
		{
			name:    "steer requires input",
			flags:   invokeFlags{steer: true},
			wantErr: "--steer requires a message argument or --input-file",
		},
		{name: "cancel accepts empty input", flags: invokeFlags{cancel: true}},
		{
			name:    "cancel rejects input",
			flags:   invokeFlags{cancel: true, message: "hello"},
			wantErr: "--resume and --cancel do not accept a message or --input-file",
		},
		{
			name:    "resume and steer are exclusive",
			flags:   invokeFlags{resume: true, steer: true, message: "hello"},
			wantErr: "--resume, --steer, and --cancel are mutually exclusive",
		},
		{
			name:    "steer and cancel are exclusive",
			flags:   invokeFlags{steer: true, cancel: true, message: "hello"},
			wantErr: "--resume, --steer, and --cancel are mutually exclusive",
		},
		{
			name:    "background and resume are exclusive",
			flags:   invokeFlags{resumable: true, resume: true, message: "hello"},
			wantErr: "--resumable cannot be combined with --resume, --steer, or --cancel",
		},
		{
			name:    "background and steer are exclusive",
			flags:   invokeFlags{resumable: true, steer: true, message: "hello"},
			wantErr: "--resumable cannot be combined with --resume, --steer, or --cancel",
		},
		{
			name:    "background and cancel are exclusive",
			flags:   invokeFlags{resumable: true, cancel: true, message: "hello"},
			wantErr: "--resumable cannot be combined with --resume, --steer, or --cancel",
		},
		{
			name:    "continue rejects session id",
			flags:   invokeFlags{resume: true},
			changed: map[string]string{"session-id": "sess_123"},
			wantErr: "use the saved session and conversation",
		},
		{
			name:    "continue rejects new session",
			flags:   invokeFlags{resume: true},
			changed: map[string]string{"new-session": "true"},
			wantErr: "use the saved session and conversation",
		},
		{
			name:    "steer rejects conversation id",
			flags:   invokeFlags{steer: true, message: "hello"},
			changed: map[string]string{"conversation-id": "conv_123"},
			wantErr: "use the saved session and conversation",
		},
		{
			name:    "cancel rejects conversation id",
			flags:   invokeFlags{cancel: true},
			changed: map[string]string{"conversation-id": "conv_123"},
			wantErr: "use the saved session and conversation",
		},
		{
			name:    "cancel rejects new conversation",
			flags:   invokeFlags{cancel: true},
			changed: map[string]string{"new-conversation": "true"},
			wantErr: "use the saved session and conversation",
		},
		{
			name:    "resume rejects timeout",
			flags:   invokeFlags{resume: true},
			changed: map[string]string{"timeout": "1"},
			wantErr: "--timeout is not supported with --resume, --steer, or --cancel",
		},
		{
			name:    "steer rejects timeout",
			flags:   invokeFlags{steer: true, message: "hello"},
			changed: map[string]string{"timeout": "1"},
			wantErr: "--timeout is not supported with --resume, --steer, or --cancel",
		},
		{
			name:    "cancel rejects timeout",
			flags:   invokeFlags{cancel: true},
			changed: map[string]string{"timeout": "1"},
			wantErr: "--timeout is not supported with --resume, --steer, or --cancel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := newInvokeCommand(nil)
			for name, value := range tt.changed {
				require.NoError(t, cmd.Flags().Set(name, value))
			}

			err := validateInvokeOperationFlags(cmd, &tt.flags)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestParseInvokeArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flags       invokeFlags
		args        []string
		wantName    string
		wantMessage string
	}{
		{name: "single positional is message", args: []string{"hello"}, wantMessage: "hello"},
		{
			name:     "single positional with input file is agent",
			flags:    invokeFlags{inputFile: "request.json"},
			args:     []string{"agent"},
			wantName: "agent",
		},
		{
			name:     "single positional with resume is agent",
			flags:    invokeFlags{resume: true},
			args:     []string{"agent"},
			wantName: "agent",
		},
		{
			name:        "single positional with steer is message",
			flags:       invokeFlags{steer: true},
			args:        []string{"revised requirements"},
			wantMessage: "revised requirements",
		},
		{
			name:     "single positional with cancel is agent",
			flags:    invokeFlags{cancel: true},
			args:     []string{"agent"},
			wantName: "agent",
		},
		{
			name:        "two positionals are agent and message",
			args:        []string{"agent", "hello"},
			wantName:    "agent",
			wantMessage: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parseInvokeArgs(&tt.flags, tt.args)
			assert.Equal(t, tt.wantName, tt.flags.name)
			assert.Equal(t, tt.wantMessage, tt.flags.message)
		})
	}
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
			args: []string{"--resumable", "--local", "hello"},
			want: "saved-work operations are supported only for remote agents",
		},
		{
			name: "rejects explicit invocations protocol",
			args: []string{"--resumable", "--protocol", "invocations", "hello"},
			want: "resumable operations are not supported with the invocations protocol",
		},
		{
			name:     "rejects explicit timeout",
			args:     []string{"--resumable", "--timeout", "1", "hello"},
			want:     "--timeout cannot be used with --resumable",
			wantCode: exterrors.CodeConflictingArguments,
		},
		{
			name:     "rejects explicitly set default timeout",
			args:     []string{"--resumable", "--timeout", "1800", "hello"},
			want:     "--timeout cannot be used with --resumable",
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

func TestInvokeCommandRejectsInvocationsResumeWithInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "message",
			args: []string{"agent", "revised requirements", "--protocol", "invocations", "--resume"},
		},
		{
			name: "input file",
			args: []string{"--protocol", "invocations", "--resume", "--input-file", "request.json"},
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
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
			assert.Equal(t, "--resume and --cancel do not accept a message or --input-file", localErr.Message)
			assert.Equal(
				t,
				"remove the input; --resume reconnects to saved work and --cancel cancels saved Responses",
				localErr.Suggestion,
			)
		})
	}
}

func TestInvokeCommandRejectsInvocationResumeWithAgentEndpoint(t *testing.T) {
	isolateFromAzdDaemon(t)

	cmd := newInvokeCommand(nil)
	cmd.SetArgs([]string{
		"--resume",
		"--agent-endpoint",
		"https://acct.services.ai.azure.com/api/projects/proj/agents/test-agent/endpoint/protocols/invocations",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
	assert.Equal(t, "Invocations --resume is not supported with --agent-endpoint", localErr.Message)
	assert.Equal(t, "run from an azd project so the saved Invocation can be resolved", localErr.Suggestion)
}

func TestInvokeCommandInvocationResumeDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		extCtx     *azdext.ExtensionContext
		args       []string
		message    string
		suggestion string
	}{
		{
			name:       "local",
			args:       []string{"--protocol", "invocations", "--resume", "--local"},
			message:    "saved-work operations are supported only for remote agents",
			suggestion: "remove --local and use a deployed agent",
		},
		{
			name:       "raw output",
			extCtx:     &azdext.ExtensionContext{OutputFormat: outputRaw},
			args:       []string{"--protocol", "invocations", "--resume"},
			message:    "--output raw is not supported with saved-work operations",
			suggestion: "remove --output raw so azd can manage saved operation state",
		},
		{
			name:       "timeout",
			args:       []string{"--protocol", "invocations", "--resume", "--timeout", "1"},
			message:    "--timeout is not supported with --resume, --steer, or --cancel",
			suggestion: "remove --timeout; saved-work operations manage request timing internally",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := newInvokeCommand(tt.extCtx)
			cmd.SetArgs(tt.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)

			err := cmd.Execute()
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, tt.message, localErr.Message)
			assert.Equal(t, tt.suggestion, localErr.Suggestion)
		})
	}
}

func TestInvokeCommandBackgroundEndpointRoutesHostFailure(t *testing.T) {
	isolateFromAzdDaemon(t)

	cmd := newInvokeCommand(nil)
	cmd.SetArgs([]string{
		"--resumable",
		"--agent-endpoint",
		"https://acct.services.ai.azure.com/api/projects/proj/agents/test-agent/endpoint/protocols/openai/responses",
		"hello",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, azdext.LocalErrorCategoryInternal, localErr.Category)
	assert.Equal(t, exterrors.OpReadBackgroundResponseState, localErr.Code)
	assert.Empty(t, localErr.Suggestion)
	assert.NotContains(t, err.Error(), "--resumable is not supported with --agent-endpoint")
}

func TestClassifyBackgroundResponseStateReadError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		cause            error
		wantCategory     azdext.LocalErrorCategory
		wantCode         string
		wantMessage      string
		wantSuggestion   string
		rejectSuggestion string
	}{
		{
			name: "config error",
			cause: fmt.Errorf("read background responses: %w", &azdext.ConfigError{
				Path:   backgroundResponsesConfigPath,
				Reason: azdext.ConfigReasonInvalidFormat,
				Err:    errors.New("invalid JSON"),
			}),
			wantCategory:   azdext.LocalErrorCategoryValidation,
			wantCode:       exterrors.CodeInvalidBackgroundResponseState,
			wantMessage:    "invalid JSON",
			wantSuggestion: "azd config unset " + backgroundResponsesConfigPath,
		},
		{
			name:             "context cancellation",
			cause:            fmt.Errorf("read background responses: %w", context.Canceled),
			wantCategory:     azdext.LocalErrorCategoryUser,
			wantCode:         exterrors.CodeCancelled,
			wantMessage:      "was cancelled",
			rejectSuggestion: "config unset",
		},
		{
			name:             "permission denied",
			cause:            status.Error(codes.PermissionDenied, "access denied"),
			wantCategory:     azdext.LocalErrorCategoryInternal,
			wantCode:         exterrors.OpReadBackgroundResponseState,
			wantMessage:      "access denied",
			rejectSuggestion: "config unset",
		},
		{
			name:             "deadline exceeded",
			cause:            status.Error(codes.DeadlineExceeded, "deadline exceeded"),
			wantCategory:     azdext.LocalErrorCategoryInternal,
			wantCode:         exterrors.OpReadBackgroundResponseState,
			wantMessage:      "deadline exceeded",
			rejectSuggestion: "config unset",
		},
		{
			name:             "transient unavailable",
			cause:            status.Error(codes.Unavailable, "host unavailable"),
			wantCategory:     azdext.LocalErrorCategoryInternal,
			wantCode:         exterrors.OpReadBackgroundResponseState,
			wantMessage:      "host unavailable",
			rejectSuggestion: "config unset",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := classifyBackgroundResponseStateReadError(tt.cause)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, tt.wantCategory, localErr.Category)
			assert.Equal(t, tt.wantCode, localErr.Code)
			assert.Contains(t, localErr.Message, tt.wantMessage)
			if tt.wantSuggestion != "" {
				assert.Contains(t, localErr.Suggestion, tt.wantSuggestion)
			}
			if tt.rejectSuggestion != "" {
				assert.NotContains(t, localErr.Suggestion, tt.rejectSuggestion)
			}
		})
	}
}
