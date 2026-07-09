// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package routines

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Client options

func TestResolveRequestTimeouts(t *testing.T) {
	t.Parallel()

	readTimeout, writeTimeout := resolveRequestTimeouts(nil)
	assert.Equal(t, DefaultReadRequestTimeout, readTimeout)
	assert.Equal(t, DefaultWriteRequestTimeout, writeTimeout)

	readTimeout, writeTimeout = resolveRequestTimeouts(&ClientOptions{})
	assert.Equal(t, DefaultReadRequestTimeout, readTimeout)
	assert.Equal(t, DefaultWriteRequestTimeout, writeTimeout)

	readTimeout, writeTimeout = resolveRequestTimeouts(&ClientOptions{
		RequestTimeout: 90 * time.Second,
	})
	assert.Equal(t, 90*time.Second, readTimeout)
	assert.Equal(t, 90*time.Second, writeTimeout)
}

func TestNewHTTPClient_UsesRequestTimeout(t *testing.T) {
	t.Parallel()

	client := newHTTPClient(90 * time.Second)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, 90*time.Second, transport.ResponseHeaderTimeout)
}

type testPipelineHeaderPolicy string

func (p testPipelineHeaderPolicy) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set("X-Test-Pipeline", string(p))
	return req.Next()
}

func newTestPipeline(name string) azruntime.Pipeline {
	return azruntime.NewPipeline(
		"test",
		"v0",
		azruntime.PipelineOptions{},
		&policy.ClientOptions{
			PerCallPolicies: []policy.Policy{
				testPipelineHeaderPolicy(name),
			},
		},
	)
}

// newTestClient creates a Client with a pipeline that skips auth (no TLS
// requirement) pointing at a local httptest server.
func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Build a pipeline without bearer-token policy so plain HTTP works.
	client := &Client{
		endpoint:      srv.URL + "/api/projects/test-project",
		readPipeline:  newTestPipeline("read"),
		writePipeline: newTestPipeline("write"),
	}
	return client, srv
}

// ─── GetRoutine ──────────────────────────────────────────────────────────────

func TestGetRoutine_Success(t *testing.T) {
	t.Parallel()
	routine := Routine{Name: "my-routine", Description: "test routine", Enabled: new(true)}

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "/routines/my-routine")
		assert.Equal(t, routinesPreviewValue, r.Header.Get(routinesPreviewHeader))
		assert.Equal(t, "read", r.Header.Get("X-Test-Pipeline"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(routine)
	}))

	got, err := client.GetRoutine(t.Context(), "my-routine")
	require.NoError(t, err)
	assert.Equal(t, "my-routine", got.Name)
	assert.Equal(t, "test routine", got.Description)
	assert.True(t, *got.Enabled)
}

func TestGetRoutine_NotFound(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NotFound","message":"routine not found"}}`))
	}))

	_, err := client.GetRoutine(t.Context(), "nonexistent")
	require.Error(t, err)

	var respErr *azcore.ResponseError
	require.ErrorAs(t, err, &respErr)
	assert.Equal(t, http.StatusNotFound, respErr.StatusCode)
}

func TestGetRoutine_ContextCancellation(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Routine{Name: "x"})
	}))

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	_, err := client.GetRoutine(ctx, "x")
	require.Error(t, err)
}

// ─── ListRoutines ────────────────────────────────────────────────────────────

func TestListRoutines_SinglePage(t *testing.T) {
	t.Parallel()
	page := PagedRoutine{
		Value: []Routine{
			{Name: "r1"},
			{Name: "r2"},
		},
	}

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))

	got, err := client.ListRoutines(t.Context())
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, "r1", got[0].Name)
	assert.Equal(t, "r2", got[1].Name)
}

func TestListRoutines_MultiPage(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")

		switch call {
		case 1:
			// First page has a continuation token
			_ = json.NewEncoder(w).Encode(PagedRoutine{
				Value:             []Routine{{Name: "r1"}},
				ContinuationToken: "token-page2",
			})
		case 2:
			// Second page: verify "after" query param is passed
			assert.Contains(t, r.URL.RawQuery, "after=token-page2")
			_ = json.NewEncoder(w).Encode(PagedRoutine{
				Value: []Routine{{Name: "r2"}},
			})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	got, err := client.ListRoutines(t.Context())
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, "r1", got[0].Name)
	assert.Equal(t, "r2", got[1].Name)
}

func TestListRoutines_ServerError(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"InternalError","message":"boom"}}`))
	}))

	_, err := client.ListRoutines(t.Context())
	require.Error(t, err)
}

// ─── PutRoutine ──────────────────────────────────────────────────────────────

func TestPutRoutine_Created(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "write", r.Header.Get("X-Test-Pipeline"))

		var body Routine
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		assert.Equal(t, "new-routine", body.Name)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		body.CreatedAt = "2025-01-01T00:00:00Z"
		_ = json.NewEncoder(w).Encode(body)
	}))

	input := &Routine{Name: "new-routine", Description: "desc"}
	got, err := client.PutRoutine(t.Context(), "new-routine", input)
	require.NoError(t, err)
	assert.Equal(t, "new-routine", got.Name)
	assert.Equal(t, "2025-01-01T00:00:00Z", got.CreatedAt.String())
}

func TestPutRoutine_Updated(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Routine{Name: "existing", UpdatedAt: "2025-06-01T00:00:00Z"})
	}))

	got, err := client.PutRoutine(t.Context(), "existing", &Routine{Name: "existing"})
	require.NoError(t, err)
	assert.Equal(t, "2025-06-01T00:00:00Z", got.UpdatedAt.String())
}

func TestPutRoutine_Conflict(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"Conflict","message":"name in use"}}`))
	}))

	_, err := client.PutRoutine(t.Context(), "dup", &Routine{Name: "dup"})
	require.Error(t, err)

	var respErr *azcore.ResponseError
	require.ErrorAs(t, err, &respErr)
	assert.Equal(t, http.StatusConflict, respErr.StatusCode)
}

// ─── DeleteRoutine ───────────────────────────────────────────────────────────

func TestDeleteRoutine_OK(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Contains(t, r.URL.Path, "/routines/doomed")
		w.WriteHeader(http.StatusOK)
	}))

	err := client.DeleteRoutine(t.Context(), "doomed")
	require.NoError(t, err)
}

func TestDeleteRoutine_NoContent(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	err := client.DeleteRoutine(t.Context(), "gone")
	require.NoError(t, err)
}

func TestDeleteRoutine_NotFound(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NotFound","message":"not found"}}`))
	}))

	err := client.DeleteRoutine(t.Context(), "missing")
	require.Error(t, err)

	var respErr *azcore.ResponseError
	require.ErrorAs(t, err, &respErr)
	assert.Equal(t, http.StatusNotFound, respErr.StatusCode)
}

// ─── EnableRoutine / DisableRoutine ──────────────────────────────────────────

func TestEnableRoutine_Success(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/routines/my-routine:enable")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Routine{Name: "my-routine", Enabled: new(true)})
	}))

	got, err := client.EnableRoutine(t.Context(), "my-routine")
	require.NoError(t, err)
	assert.True(t, *got.Enabled)
}

func TestDisableRoutine_Success(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/routines/my-routine:disable")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Routine{Name: "my-routine", Enabled: new(false)})
	}))

	got, err := client.DisableRoutine(t.Context(), "my-routine")
	require.NoError(t, err)
	assert.False(t, *got.Enabled)
}

func TestEnableRoutine_ServerError(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"InternalError","message":"oops"}}`))
	}))

	_, err := client.EnableRoutine(t.Context(), "broken")
	require.Error(t, err)
}

func TestDisableRoutine_ServerError(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"InternalError","message":"oops"}}`))
	}))

	_, err := client.DisableRoutine(t.Context(), "broken")
	require.Error(t, err)
}

// ─── DispatchRoutineAsync ────────────────────────────────────────────────────

func TestDispatchRoutineAsync_Success(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/routines/my-routine:dispatch_async")

		var body DispatchRoutineRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		assert.Equal(t, "invoke_agent_responses_api", body.Payload.Type)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(DispatchRoutineResponse{
			DispatchID:          "d-123",
			ActionCorrelationID: "ac-456",
		})
	}))

	resp, err := client.DispatchRoutineAsync(t.Context(), "my-routine", &DispatchRoutineRequest{
		Payload: &RoutineDispatchPayload{
			Type:  "invoke_agent_responses_api",
			Input: "hello",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "d-123", resp.DispatchID)
	assert.Equal(t, "ac-456", resp.ActionCorrelationID)
}

func TestDispatchRoutineAsync_NilPayload(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// With nil payload, body should be empty (no Content-Type set by setJSONBody)
		assert.Equal(t, int64(0), r.ContentLength)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(DispatchRoutineResponse{DispatchID: "d-nil"})
	}))

	resp, err := client.DispatchRoutineAsync(t.Context(), "my-routine", nil)
	require.NoError(t, err)
	assert.Equal(t, "d-nil", resp.DispatchID)
}

func TestDispatchRoutineAsync_Error(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BadRequest","message":"invalid payload"}}`))
	}))

	_, err := client.DispatchRoutineAsync(t.Context(), "bad", &DispatchRoutineRequest{})
	require.Error(t, err)
}

// ─── ListRoutineRuns ─────────────────────────────────────────────────────────

func TestListRoutineRuns_SinglePage(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/routines/my-routine/runs")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PagedRoutineRun{
			Value: []RoutineRun{
				{ID: "run-1", Status: "completed"},
				{ID: "run-2", Status: "running"},
			},
		})
	}))

	runs, err := client.ListRoutineRuns(t.Context(), "my-routine", ListRoutineRunsOptions{})
	require.NoError(t, err)
	assert.Len(t, runs, 2)
}

func TestListRoutineRuns_WithTop(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		assert.Contains(t, r.URL.RawQuery, "limit=1")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PagedRoutineRun{
			Value:         []RoutineRun{{ID: "run-1"}, {ID: "run-2"}},
			NextPageToken: "page2",
		})
	}))

	runs, err := client.ListRoutineRuns(t.Context(), "my-routine", ListRoutineRunsOptions{Top: 1})
	require.NoError(t, err)
	assert.Len(t, runs, 1, "Top should cap results to 1")
	assert.Equal(t, int32(1), callCount.Load(), "client should not fetch page 2 when Top is already satisfied")
}

func TestListRoutineRuns_WithFilter(t *testing.T) {
	t.Parallel()
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.RawQuery, "filter=status+eq+%27completed%27")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PagedRoutineRun{
			Value: []RoutineRun{{ID: "run-1", Status: "completed"}},
		})
	}))

	runs, err := client.ListRoutineRuns(t.Context(), "my-routine", ListRoutineRunsOptions{
		Filter: "status eq 'completed'",
	})
	require.NoError(t, err)
	assert.Len(t, runs, 1)
}

func TestListRoutineRuns_Pagination(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")

		switch call {
		case 1:
			_ = json.NewEncoder(w).Encode(PagedRoutineRun{
				Value:         []RoutineRun{{ID: "run-1"}},
				NextPageToken: "next-tok",
			})
		case 2:
			assert.Contains(t, r.URL.RawQuery, "after=next-tok")
			_ = json.NewEncoder(w).Encode(PagedRoutineRun{
				Value: []RoutineRun{{ID: "run-2"}},
			})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	runs, err := client.ListRoutineRuns(t.Context(), "my-routine", ListRoutineRunsOptions{})
	require.NoError(t, err)
	assert.Len(t, runs, 2)
}

// ─── URL construction helpers ────────────────────────────────────────────────

func TestRoutineURL_EscapesName(t *testing.T) {
	t.Parallel()
	c := &Client{endpoint: "https://example.com/api/projects/p"}

	url := c.routineURL("has space")
	assert.Contains(t, url, "/routines/has%20space")
	assert.Contains(t, url, "api-version="+routinesAPIVersion)
}

func TestRoutineActionURL(t *testing.T) {
	t.Parallel()
	c := &Client{endpoint: "https://example.com/api/projects/p"}

	url := c.routineActionURL("my-routine", "enable")
	assert.Contains(t, url, "/routines/my-routine:enable")
}

func TestRoutineRunsURL_WithQuery(t *testing.T) {
	t.Parallel()
	c := &Client{endpoint: "https://example.com/api/projects/p"}

	url := c.routineRunsURL("r1", "limit=5", "after=tok")
	assert.Contains(t, url, "/routines/r1/runs")
	assert.Contains(t, url, "limit=5")
	assert.Contains(t, url, "after=tok")
}
