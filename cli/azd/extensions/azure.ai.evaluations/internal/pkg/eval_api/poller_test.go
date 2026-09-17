// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastPoller polls without the two-second wait a real one takes, so a test
// covering an exhausted attempt budget finishes in milliseconds.
func fastPoller(t *testing.T, get GetJobFunc, maxAttempts int) *Poller {
	t.Helper()
	p := NewPoller("op_1", "v1", get)
	p.Options = PollerOptions{Interval: time.Millisecond, MaxAttempts: maxAttempts}
	return p
}

// jobs answers each poll with the next status in turn, holding the last one.
func jobs(statuses ...string) (GetJobFunc, *atomic.Int32) {
	var calls atomic.Int32
	return func(context.Context, string, string) (*GenerationJob, error) {
		i := int(calls.Add(1)) - 1
		if i >= len(statuses) {
			i = len(statuses) - 1
		}
		return &GenerationJob{ID: "op_1", Status: statuses[i]}, nil
	}, &calls
}

// The service spells terminal states several ways and does not agree with
// itself on case. Missing one leaves the CLI polling a job that will never
// change again, until the attempt budget runs out minutes later.
func TestJobStatusTerminalAndFailed(t *testing.T) {
	cases := []struct {
		raw      string
		terminal bool
		failed   bool
	}{
		{"running", false, false},
		{"queued", false, false},
		{"", false, false}, // an absent status is a job that has not started
		{"completed", true, false},
		{"succeeded", true, false},
		{"Succeeded", true, false}, // the service does not agree with itself on case
		{"COMPLETED", true, false},
		{"failed", true, true},
		{"cancelled", true, true}, // both spellings are in use
		{"canceled", true, true},
		{"Cancelled", true, true},
	}

	for _, tc := range cases {
		status := ParseJobStatus(tc.raw)
		assert.Equal(t, tc.terminal, status.IsTerminal(), "IsTerminal(%q)", tc.raw)
		assert.Equal(t, tc.failed, status.IsFailed(), "IsFailed(%q)", tc.raw)
	}

	assert.Equal(t, JobStatusRunning, ParseJobStatus(""),
		"a job with no status yet is running, not finished")
	assert.Equal(t, "succeeded", ParseJobStatus("Succeeded").String())
}

// A job that reaches a success state is returned, and polling stops there
// rather than continuing to spend attempts.
func TestPollerReturnsOnSuccess(t *testing.T) {
	get, calls := jobs("running", "running", "completed")
	job, err := fastPoller(t, get, 10).Poll(context.Background())

	require.NoError(t, err)
	require.NotNil(t, job)
	assert.Equal(t, "completed", job.Status)
	assert.Equal(t, int32(3), calls.Load(), "polling stops at the terminal state")
}

// A failure has to carry the service's own message, since "job failed" alone
// leaves the user with nothing to act on.
func TestPollerReportsTheFailureMessage(t *testing.T) {
	get := func(context.Context, string, string) (*GenerationJob, error) {
		return &GenerationJob{
			ID:     "op_1",
			Status: "failed",
			Error:  &JobError{Message: "the model deployment was not found"},
		}, nil
	}

	_, err := fastPoller(t, get, 5).Poll(context.Background())

	var failed *JobFailedError
	require.ErrorAs(t, err, &failed, "the caller inspects the job, so the type has to survive")
	assert.Equal(t, JobStatusFailed, failed.Status)
	assert.Contains(t, err.Error(), "the model deployment was not found")
}

// A cancelled job is finished but not successful, so it must not be returned
// as a job whose output can be read.
func TestPollerTreatsCancellationAsFailure(t *testing.T) {
	get, _ := jobs("cancelled")
	job, err := fastPoller(t, get, 5).Poll(context.Background())

	require.Error(t, err)
	assert.Nil(t, job)
	var failed *JobFailedError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, JobStatusCancelled, failed.Status)
}

// A job with no error object still has to say something, or the failure
// surfaces as an empty line.
func TestJobFailedErrorWithoutAMessage(t *testing.T) {
	err := &JobFailedError{Status: JobStatusFailed}
	assert.Contains(t, err.Error(), "failed")
}

// Throttling and server faults are the service being busy, not the job being
// broken: giving up on one would fail a run that was going to succeed.
func TestPollerRetriesTransientErrors(t *testing.T) {
	var calls atomic.Int32
	get := func(context.Context, string, string) (*GenerationJob, error) {
		switch calls.Add(1) {
		case 1:
			return nil, &azcore.ResponseError{StatusCode: http.StatusTooManyRequests}
		case 2:
			return nil, &azcore.ResponseError{StatusCode: http.StatusBadGateway}
		case 3:
			return nil, errors.New("read tcp: connection reset by peer")
		default:
			return &GenerationJob{ID: "op_1", Status: "succeeded"}, nil
		}
	}

	job, err := fastPoller(t, get, 10).Poll(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "succeeded", job.Status)
	assert.Equal(t, int32(4), calls.Load())
}

// A real failure — a deleted job, a bad token — is not worth retrying for the
// rest of the budget, so it is returned at once.
func TestPollerStopsOnAPermanentError(t *testing.T) {
	var calls atomic.Int32
	get := func(context.Context, string, string) (*GenerationJob, error) {
		calls.Add(1)
		return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}

	_, err := fastPoller(t, get, 10).Poll(context.Background())

	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load(), "a 404 will not become a 200")
}

// A job that never finishes has to end as a timeout naming the operation, so
// the user can go look it up rather than being told nothing.
func TestPollerTimesOut(t *testing.T) {
	get, calls := jobs("running")
	_, err := fastPoller(t, get, 3).Poll(context.Background())

	var timeout *PollerTimeoutError
	require.ErrorAs(t, err, &timeout)
	assert.Equal(t, "op_1", timeout.OperationID)
	assert.Equal(t, 3, timeout.Attempts)
	assert.Contains(t, err.Error(), "op_1")
	assert.Equal(t, int32(3), calls.Load(), "the budget is attempts, not retries after the first")
}

// Ctrl-C during a wait has to return promptly rather than after the interval,
// and the context's own error is what says why.
func TestPollerHonoursContextCancellation(t *testing.T) {
	get, _ := jobs("running")
	p := NewPoller("op_1", "v1", get)
	p.Options = PollerOptions{Interval: time.Hour, MaxAttempts: 10}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { _, err := p.Poll(ctx); done <- err }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Poll ignored a cancelled context and waited out the interval")
	}
}

// Progress reporting is what the user sees during a long run, so the callback
// fires on every poll and not only the last.
func TestPollerReportsEachStatus(t *testing.T) {
	get, _ := jobs("queued", "running", "succeeded")
	p := fastPoller(t, get, 10)

	var seen []JobStatus
	p.OnPoll = func(s JobStatus) { seen = append(seen, s) }

	_, err := p.Poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []JobStatus{"queued", "running", "succeeded"}, seen)
}

// Polling an empty id would ask the service about nothing, forever. It is the
// caller's bug and is worth naming before the first request.
func TestPollerRejectsAnEmptyOperationID(t *testing.T) {
	_, err := NewPoller("", "v1", nil).Poll(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation ID")
}

// The defaults bound a run at roughly ten minutes. A shorter budget would
// abandon generation jobs that legitimately take that long.
func TestDefaultPollerOptions(t *testing.T) {
	opts := DefaultPollerOptions()
	assert.Equal(t, 2*time.Second, opts.Interval)
	assert.Equal(t, 300, opts.MaxAttempts)
	assert.GreaterOrEqual(t, opts.Interval*time.Duration(opts.MaxAttempts), 10*time.Minute)
}
