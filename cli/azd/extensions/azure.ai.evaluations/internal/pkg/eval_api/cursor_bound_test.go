// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stuckCursorServer always answers has_more with the same last_id, which is
// what a service in a bad state does. Without a bound the client walks it
// forever, holding the command open and growing the slice until the process
// dies -- so this test would hang rather than fail if the guard were removed.
func stuckCursorServer(t *testing.T, calls *atomic.Int64) *EvalClient {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := json.Marshal(map[string]any{
			"data": []map[string]any{
				{"id": "item_1", "status": "pass"},
			},
			// The cursor never advances.
			"has_more": true,
			"last_id":  "cursor_that_never_moves",
		})
		assert.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))
}

// TestListOutputItemsStopsOnACursorThatNeverMoves pins termination. The
// deadline exists so a regression reports a failure instead of hanging the
// whole suite.
//
// Termination is not enough on its own: the walk used to stop and hand back the
// rows it had, which a caller could not tell from the whole run. These rows
// drive `run output list`, the mean scores and every export, so an unfinished
// walk is now an error.
func TestListOutputItemsStopsOnACursorThatNeverMoves(t *testing.T) {
	var calls atomic.Int64
	client := stuckCursorServer(t, &calls)

	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		_, err = client.ListOutputItems(t.Context(), "eval_1", "run_1", 0)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the walk never terminated on a repeating cursor")
	}

	require.Error(t, err, "the service never advanced, so the rows read are partial")
	assert.Contains(t, err.Error(), "incomplete")
	assert.Equal(t, int64(2), calls.Load(),
		"the repeat is visible on the second read, so the walk stops there")
}

// A cursor that always advances defeats the repeat check, so the page ceiling
// is the only thing left holding the walk open. A service paging one row at a
// time forever would otherwise never return.
func TestListOutputItemsStopsAtThePageCeiling(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		body, err := json.Marshal(map[string]any{
			"data":     []map[string]any{{"id": fmt.Sprintf("item_%d", n), "status": "pass"}},
			"has_more": true,
			// Always a new cursor, so `seen` never fires.
			"last_id": fmt.Sprintf("cursor_%d", n),
		})
		assert.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	client := NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		_, err = client.ListOutputItems(t.Context(), "eval_1", "run_1", 0)
	}()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the walk never terminated on an endlessly advancing cursor")
	}

	require.Error(t, err, "the ceiling was reached with more to fetch, so the rows read are partial")
	assert.Contains(t, err.Error(), "incomplete")
	assert.Equal(t, int64(maxPages), calls.Load(),
		"the walk has to stop at the ceiling rather than trust the service to end it")
}
