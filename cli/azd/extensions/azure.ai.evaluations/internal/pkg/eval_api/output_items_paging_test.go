// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A run's rows are what `run output list`, `--failed-only` and `export` all
// read, and what the mean score per evaluator is averaged over. Stopping at
// the first page would report a sample of the run as the run, with nothing on
// screen to say rows were missing.
func TestListOutputItemsFollowsTheCursor(t *testing.T) {
	var requests atomic.Int32

	client := newRecordingClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		after := r.URL.Query().Get("after")

		switch n {
		case 1:
			assert.Empty(t, after, "the first page is not asked for by cursor")
			writeJSON(t, w, map[string]any{
				"data": []map[string]any{
					{"id": "a", "status": "completed"},
					{"id": "b", "status": "completed"},
				},
				"has_more": true,
				"last_id":  "b",
			})
		case 2:
			assert.Equal(t, "b", after, "the next page is asked for from the last id")
			writeJSON(t, w, map[string]any{
				"data":     []map[string]any{{"id": "c", "status": "completed"}},
				"has_more": false,
			})
		default:
			t.Errorf("asked for a page after the service said there were none")
			http.Error(w, "unexpected page request", http.StatusInternalServerError)
		}
	})

	list, err := client.ListOutputItems(context.Background(), "eval_1", "run_1", 0)

	require.NoError(t, err)
	require.Len(t, list.Data, 3, "every page's rows belong to the run")
	assert.Equal(t, []string{"a", "b", "c"}, ids(list.Data))
	assert.EqualValues(t, 2, requests.Load())
}

// A service that answers one page and says nothing about more is the shape
// this client was written against, and must still work exactly as before.
func TestListOutputItemsStopsWithoutACursor(t *testing.T) {
	var requests atomic.Int32

	client := newRecordingClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{{"id": "only", "status": "completed"}},
		})
	})

	list, err := client.ListOutputItems(context.Background(), "eval_1", "run_1", 0)

	require.NoError(t, err)
	require.Len(t, list.Data, 1)
	assert.EqualValues(t, 1, requests.Load(), "one page, one request")
}

// --limit is a cap on rows, not on requests: fetching past it would spend the
// caller's time on rows they said they did not want.
func TestListOutputItemsHonoursTheLimitAcrossPages(t *testing.T) {
	var requests atomic.Int32

	client := newRecordingClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{
				{"id": fmt.Sprintf("row-%d", requests.Load()), "status": "completed"},
			},
			"has_more": true,
			"last_id":  fmt.Sprintf("row-%d", requests.Load()),
		})
	})

	list, err := client.ListOutputItems(context.Background(), "eval_1", "run_1", 2)

	require.NoError(t, err)
	require.Len(t, list.Data, 2)
	assert.EqualValues(t, 2, requests.Load(), "the cap stops the paging")
}

// A page that claims more but carries nothing would otherwise loop forever.
func TestListOutputItemsStopsOnAnEmptyPage(t *testing.T) {
	var requests atomic.Int32

	client := newRecordingClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assert.Less(t, requests.Load(), int32(5), "the client is looping")
		writeJSON(t, w, map[string]any{
			"data":     []map[string]any{},
			"has_more": true,
			"last_id":  "x",
		})
	})

	list, err := client.ListOutputItems(context.Background(), "eval_1", "run_1", 0)

	require.NoError(t, err)
	assert.Empty(t, list.Data)
	assert.EqualValues(t, 1, requests.Load())
}

// writeJSON is called from the server's goroutine, so it asserts rather than
// requires: FailNow off the test's own goroutine aborts mid-response and the
// failure lands on whichever test happens to be running.
func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	assert.NoError(t, json.NewEncoder(w).Encode(body))
}

func ids(items []OutputItem) []string {
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.ID)
	}
	return out
}
