// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// JobStatus
// ---------------------------------------------------------------------------

func TestParseJobStatus(t *testing.T) {
	t.Parallel()

	assert.Equal(t, JobStatusRunning, ParseJobStatus(""))
	assert.Equal(t, JobStatusCompleted, ParseJobStatus("completed"))
	assert.Equal(t, JobStatusCompleted, ParseJobStatus("Completed"))
	assert.Equal(t, JobStatusFailed, ParseJobStatus("Failed"))
	assert.Equal(t, JobStatus("pending"), ParseJobStatus("pending"))
}

func TestJobStatus_IsTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status   string
		terminal bool
	}{
		{"completed", true},
		{"Completed", true},
		{"succeeded", true},
		{"failed", true},
		{"cancelled", true},
		{"canceled", true},
		{"running", false},
		{"pending", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.terminal, ParseJobStatus(tt.status).IsTerminal())
		})
	}
}

func TestJobStatus_IsFailed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status string
		failed bool
	}{
		{"failed", true},
		{"Failed", true},
		{"cancelled", true},
		{"canceled", true},
		{"completed", false},
		{"succeeded", false},
		{"running", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.failed, ParseJobStatus(tt.status).IsFailed())
		})
	}
}

// ---------------------------------------------------------------------------
// Poller
// ---------------------------------------------------------------------------

func TestPoller_EmptyOperationID(t *testing.T) {
	t.Parallel()

	p := NewPoller("", "v1", func(ctx context.Context, id, ver string) (*GenerationJob, error) {
		return nil, nil
	})
	_, err := p.Poll(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation ID is empty")
}

func TestPoller_CompletedImmediately(t *testing.T) {
	t.Parallel()

	calls := 0
	p := NewPoller("op-1", "v1", func(ctx context.Context, id, ver string) (*GenerationJob, error) {
		calls++
		return &GenerationJob{ID: id, Status: "completed"}, nil
	})
	p.Options.Interval = time.Millisecond

	job, err := p.Poll(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "op-1", job.ID)
	assert.Equal(t, 1, calls)
}

func TestPoller_SucceededAfterPending(t *testing.T) {
	t.Parallel()

	calls := 0
	p := NewPoller("op-2", "v1", func(ctx context.Context, id, ver string) (*GenerationJob, error) {
		calls++
		if calls < 3 {
			return &GenerationJob{ID: id, Status: "running"}, nil
		}
		return &GenerationJob{ID: id, Status: "succeeded"}, nil
	})
	p.Options.Interval = time.Millisecond

	job, err := p.Poll(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "succeeded", job.Status)
	assert.Equal(t, 3, calls)
}

func TestPoller_FailedReturnsJobFailedError(t *testing.T) {
	t.Parallel()

	p := NewPoller("op-3", "v1", func(ctx context.Context, id, ver string) (*GenerationJob, error) {
		return &GenerationJob{ID: id, Status: "failed"}, nil
	})
	p.Options.Interval = time.Millisecond

	_, err := p.Poll(t.Context())
	require.Error(t, err)

	var jfe *JobFailedError
	require.True(t, errors.As(err, &jfe))
	assert.Equal(t, JobStatusFailed, jfe.Status)
	assert.Equal(t, "op-3", jfe.Job.ID)
}

func TestPoller_APIError(t *testing.T) {
	t.Parallel()

	p := NewPoller("op-4", "v1", func(ctx context.Context, id, ver string) (*GenerationJob, error) {
		return nil, fmt.Errorf("network error")
	})
	p.Options.Interval = time.Millisecond

	_, err := p.Poll(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network error")
}

func TestPoller_MaxAttemptsExhausted(t *testing.T) {
	t.Parallel()

	p := NewPoller("op-5", "v1", func(ctx context.Context, id, ver string) (*GenerationJob, error) {
		return &GenerationJob{ID: id, Status: "running"}, nil
	})
	p.Options.Interval = time.Millisecond
	p.Options.MaxAttempts = 3

	_, err := p.Poll(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not complete")
	timeoutErr, ok := errors.AsType[*PollerTimeoutError](err)
	require.True(t, ok)
	assert.Equal(t, "op-5", timeoutErr.OperationID)
	assert.Equal(t, 3, timeoutErr.Attempts)
}

func TestPoller_ContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	p := NewPoller("op-6", "v1", func(ctx context.Context, id, ver string) (*GenerationJob, error) {
		return &GenerationJob{ID: id, Status: "running"}, nil
	})
	p.Options.Interval = time.Millisecond

	_, err := p.Poll(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPoller_OnPollCallback(t *testing.T) {
	t.Parallel()

	calls := 0
	var observed []JobStatus

	p := NewPoller("op-7", "v1", func(ctx context.Context, id, ver string) (*GenerationJob, error) {
		calls++
		if calls < 2 {
			return &GenerationJob{ID: id, Status: "running"}, nil
		}
		return &GenerationJob{ID: id, Status: "completed"}, nil
	})
	p.Options.Interval = time.Millisecond
	p.OnPoll = func(status JobStatus) {
		observed = append(observed, status)
	}

	_, err := p.Poll(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []JobStatus{JobStatusRunning, JobStatusCompleted}, observed)
}

// ---------------------------------------------------------------------------
// JobFailedError
// ---------------------------------------------------------------------------

func TestJobFailedError_Error(t *testing.T) {
	t.Parallel()

	e := &JobFailedError{
		Job:    &GenerationJob{ID: "op-1"},
		Status: JobStatusFailed,
	}
	assert.Contains(t, e.Error(), "failed")
}
