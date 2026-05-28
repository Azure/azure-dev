// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package routines

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCredential satisfies azcore.TokenCredential for tests so we can drive
// the real pipeline against an httptest server without going through azidentity.
type fakeCredential struct{}

func (fakeCredential) GetToken(
	_ context.Context, _ policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "fake-token",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

// requestLog records every request URL the test server sees so we can assert
// the second-page request uses the `?after=<last_id>` cursor introduced by
// azure-rest-api-specs PR #43498 instead of the previous `?pageToken=` shape.
type requestLog struct {
	mu   sync.Mutex
	urls []string
}

func (r *requestLog) record(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.urls = append(r.urls, s)
}

func (r *requestLog) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.urls))
	copy(out, r.urls)
	return out
}

// TestClient_ListRoutines_FollowsSpecCursor verifies the full
// envelope-decode + cursor-pagination flow against an httptest server.
// Regression cover for issue #8421 Bug 1.
func TestClient_ListRoutines_FollowsSpecCursor(t *testing.T) {
	t.Parallel()
	log := &requestLog{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r.URL.String())
		w.Header().Set("Content-Type", "application/json")

		// Decide the page based on the `?after=` cursor.
		after := r.URL.Query().Get("after")
		var body any
		switch after {
		case "":
			// First page — service returns spec-shaped envelope.
			body = map[string]any{
				"data": []map[string]any{
					{"name": "r-1"},
					{"name": "r-2"},
				},
				"first_id": "r-1",
				"last_id":  "r-2",
				"has_more": true,
			}
		case "r-2":
			// Second page — terminal, has_more=false.
			body = map[string]any{
				"data": []map[string]any{
					{"name": "r-3"},
				},
				"first_id": "r-3",
				"last_id":  "r-3",
				"has_more": false,
			}
		default:
			http.Error(w, "unexpected cursor", http.StatusBadRequest)
			return
		}

		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	client := newClient(srv.URL, fakeCredential{}, true)

	got, err := client.ListRoutines(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"r-1", "r-2", "r-3"},
		[]string{got[0].Name, got[1].Name, got[2].Name})

	urls := log.snapshot()
	require.Len(t, urls, 2, "expected exactly two paginated requests")
	assert.Equal(t, "/routines?api-version=v1", urls[0])
	assert.Equal(t, "/routines?api-version=v1&after=r-2", urls[1],
		"second-page request must use the AgentsPagedResult `?after=<last_id>` cursor, "+
			"not the deprecated `?pageToken=` query")
}

// TestClient_ListRoutineRuns_FollowsSpecCursor mirrors the routines pagination
// regression test for the run-history endpoint (issue #8421 Bug 2).
func TestClient_ListRoutineRuns_FollowsSpecCursor(t *testing.T) {
	t.Parallel()
	log := &requestLog{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r.URL.String())
		w.Header().Set("Content-Type", "application/json")

		after := r.URL.Query().Get("after")
		var body any
		switch after {
		case "":
			body = map[string]any{
				"data": []map[string]any{
					{"id": "run-1", "status": "Finished", "phase": "completed"},
					{"id": "run-2", "status": "Finished", "phase": "completed"},
				},
				"first_id": "run-1",
				"last_id":  "run-2",
				"has_more": true,
			}
		case "run-2":
			body = map[string]any{
				"data": []map[string]any{
					{"id": "run-3", "status": "Finished", "phase": "completed"},
				},
				"first_id": "run-3",
				"last_id":  "run-3",
				"has_more": false,
			}
		default:
			http.Error(w, "unexpected cursor", http.StatusBadRequest)
			return
		}

		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	client := newClient(srv.URL, fakeCredential{}, true)

	got, err := client.ListRoutineRuns(
		context.Background(), "e2e-timer-matrix", ListRoutineRunsOptions{},
	)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"run-1", "run-2", "run-3"},
		[]string{got[0].ID, got[1].ID, got[2].ID})

	urls := log.snapshot()
	require.Len(t, urls, 2, "expected exactly two paginated requests")
	assert.Equal(t, "/routines/e2e-timer-matrix/runs?api-version=v1", urls[0])
	assert.Equal(t,
		"/routines/e2e-timer-matrix/runs?api-version=v1&after=run-2", urls[1],
		"second-page request must use `?after=` cursor instead of `?pageToken=`")
}

// TestClient_ListRoutineRuns_TopUsesLimit confirms the client emits the new
// `?limit=` parameter (replacing the deprecated `?maxResults=`) when the
// caller caps the page size via opts.Top.
func TestClient_ListRoutineRuns_TopUsesLimit(t *testing.T) {
	t.Parallel()
	log := &requestLog{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":     []map[string]any{{"id": "run-1"}},
			"has_more": false,
		})
	}))
	defer srv.Close()

	client := newClient(srv.URL, fakeCredential{}, true)
	_, err := client.ListRoutineRuns(
		context.Background(), "e2e-timer-matrix",
		ListRoutineRunsOptions{Top: 5, Filter: "status eq 'Finished'"},
	)
	require.NoError(t, err)

	urls := log.snapshot()
	require.Len(t, urls, 1)
	assert.Contains(t, urls[0], "limit=5",
		"client must use spec-mandated `?limit=` query parameter")
	assert.NotContains(t, urls[0], "maxResults",
		"client must not use the deprecated `?maxResults=` query parameter")
	assert.Contains(t, urls[0], "filter=status+eq+%27Finished%27",
		"filter query must be URL-encoded and preserved")
}

// TestClient_ListRoutines_LegacyEnvelope_NextLink confirms the rollout
// fallback: regions that have not yet shipped #43498 still emit the legacy
// `{ value, nextLink }` envelope and the client must continue to drain it.
func TestClient_ListRoutines_LegacyEnvelope_NextLink(t *testing.T) {
	t.Parallel()
	log := &requestLog{}

	var serverURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r.URL.String())
		w.Header().Set("Content-Type", "application/json")

		// Distinguish the two pages via the explicit `legacy=2` marker since
		// the legacy envelope returns an absolute nextLink URL.
		var body any
		if r.URL.Query().Get("legacy") == "2" {
			body = map[string]any{
				"value": []map[string]any{{"name": "legacy-r-2"}},
			}
		} else {
			body = map[string]any{
				"value":    []map[string]any{{"name": "legacy-r-1"}},
				"nextLink": serverURL + "/routines?api-version=v1&legacy=2",
			}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	serverURL = srv.URL

	client := newClient(srv.URL, fakeCredential{}, true)
	got, err := client.ListRoutines(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "legacy-r-1", got[0].Name)
	assert.Equal(t, "legacy-r-2", got[1].Name)

	urls := log.snapshot()
	require.Len(t, urls, 2)
	assert.Equal(t, "/routines?api-version=v1&legacy=2", urls[1],
		"client must follow the legacy `nextLink` URL when no spec cursor is present")
}
