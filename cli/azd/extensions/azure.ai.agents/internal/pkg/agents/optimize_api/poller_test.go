// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPollerTestClient(serverURL string) *OptimizeClient {
	pipeline := runtime.NewPipeline(
		"test",
		"v0.0.0",
		runtime.PipelineOptions{},
		&policy.ClientOptions{},
	)
	return &OptimizeClient{
		endpoint: serverURL,
		pipeline: pipeline,
	}
}

func TestPoller_PollsUntilCompleted(t *testing.T) {
	t.Parallel()

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		status := StatusRunning
		if n >= 3 {
			status = StatusCompleted
		}
		_ = json.NewEncoder(w).Encode(OptimizeJobStatus{
			OperationID: "op-1",
			Status:      status,
			Progress: &JobProgress{
				CurrentIteration: int(n),
			},
		})
	}))
	defer server.Close()

	var progressCalls int32
	poller := &Poller{
		Client:      newPollerTestClient(server.URL),
		OperationID: "op-1",
		Interval:    10 * time.Millisecond,
		OnProgress: func(_ *OptimizeJobStatus) {
			atomic.AddInt32(&progressCalls, 1)
		},
	}

	result, err := poller.PollUntilDone(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, result.Status)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&callCount), int32(3))
	assert.GreaterOrEqual(t, atomic.LoadInt32(&progressCalls), int32(3))
}

func TestPoller_PollsUntilFailed(t *testing.T) {
	t.Parallel()

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		status := StatusRunning
		if n >= 2 {
			status = StatusFailed
		}
		_ = json.NewEncoder(w).Encode(OptimizeJobStatus{
			OperationID: "op-fail",
			Status:      status,
			Error: &JobError{
				Code:    "InternalError",
				Message: "something broke",
			},
		})
	}))
	defer server.Close()

	poller := &Poller{
		Client:      newPollerTestClient(server.URL),
		OperationID: "op-fail",
		Interval:    10 * time.Millisecond,
	}

	result, err := poller.PollUntilDone(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, result.Status)
}

func TestPoller_ContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(OptimizeJobStatus{
			OperationID: "op-cancel",
			Status:      StatusRunning,
		})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	poller := &Poller{
		Client:      newPollerTestClient(server.URL),
		OperationID: "op-cancel",
		Interval:    10 * time.Millisecond,
	}

	result, err := poller.PollUntilDone(ctx)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestPoller_OnProgressCalled(t *testing.T) {
	t.Parallel()

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		status := StatusRunning
		if n >= 2 {
			status = StatusCompleted
		}
		_ = json.NewEncoder(w).Encode(OptimizeJobStatus{
			OperationID: "op-prog",
			Status:      status,
		})
	}))
	defer server.Close()

	var statuses []string
	poller := &Poller{
		Client:      newPollerTestClient(server.URL),
		OperationID: "op-prog",
		Interval:    10 * time.Millisecond,
		OnProgress: func(s *OptimizeJobStatus) {
			statuses = append(statuses, s.Status)
		},
	}

	result, err := poller.PollUntilDone(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, result.Status)
	assert.GreaterOrEqual(t, len(statuses), 2)
	assert.Equal(t, StatusCompleted, statuses[len(statuses)-1])
}

func TestPoller_TransientRetryThenSuccess(t *testing.T) {
	t.Parallel()

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n <= 3 {
			// First 3 calls return 500 (transient).
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error": "server error"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(OptimizeJobStatus{
			OperationID: "op-retry",
			Status:      StatusCompleted,
		})
	}))
	defer server.Close()

	poller := &Poller{
		Client:      newPollerTestClient(server.URL),
		OperationID: "op-retry",
		Interval:    10 * time.Millisecond,
	}

	result, err := poller.PollUntilDone(t.Context())
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, result.Status)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&callCount), int32(4))
}

func TestPoller_TransientRetryExhausted(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Always return 500.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "server error"}`))
	}))
	defer server.Close()

	poller := &Poller{
		Client:      newPollerTestClient(server.URL),
		OperationID: "op-exhaust",
		Interval:    10 * time.Millisecond,
		MaxAttempts: 20, // low cap to keep test fast
	}

	_, err := poller.PollUntilDone(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consecutive transient errors")
}

func TestPoller_NilClient(t *testing.T) {
	t.Parallel()
	poller := &Poller{OperationID: "op-1"}
	_, err := poller.PollUntilDone(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Client is nil")
}

func TestPoller_EmptyOperationID(t *testing.T) {
	t.Parallel()
	poller := &Poller{Client: &OptimizeClient{}}
	_, err := poller.PollUntilDone(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OperationID is empty")
}
