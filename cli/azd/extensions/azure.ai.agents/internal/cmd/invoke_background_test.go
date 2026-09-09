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
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInvokeCommandResumableFlagRegistered(t *testing.T) {
	t.Parallel()

	flags := newInvokeCommand(nil).Flags()
	flag := flags.Lookup("resumable")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
	assert.Nil(t, flags.Lookup("background"))
}

type orderingResponseStore struct {
	saved        bool
	saveErr      error
	saves        []savedBackgroundResponse
	saveContexts []context.Context
}

func (s *orderingResponseStore) Get(context.Context, string) (*savedBackgroundResponse, error) {
	return nil, nil
}

func (s *orderingResponseStore) Save(ctx context.Context, _ string, record savedBackgroundResponse) error {
	s.saveContexts = append(s.saveContexts, ctx)
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
			want: "supported only for remote Responses agents",
		},
		{
			name: "rejects explicit invocations protocol",
			args: []string{"--resumable", "--protocol", "invocations", "hello"},
			want: "--resumable is not supported with the invocations protocol",
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
