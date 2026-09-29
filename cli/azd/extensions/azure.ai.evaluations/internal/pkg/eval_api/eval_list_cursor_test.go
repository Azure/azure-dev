// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A walked listing must not hand back the cursor of the page it stopped on.
//
// `eval list` returns HasMore and LastID from whatever it got, so that a script
// can resume with --after. ListOpenAIEvals walks every page instead, and there
// is nothing left to resume from: propagating the last page's cursor would
// offer one that returns the rows the caller already has.
func TestAWalkedEvalListingCarriesNoCursor(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		body := map[string]any{
			"data":     []map[string]any{{"id": "eval_" + r.URL.Query().Get("after")}},
			"has_more": page == 1,
			"last_id":  "cursor_1",
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(body))
	}))
	t.Cleanup(srv.Close)

	client := NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	list, err := client.ListOpenAIEvals(t.Context(), 0)
	require.NoError(t, err)
	require.Len(t, list.Data, 2, "both pages have to arrive, or the walk is not what is under test")

	assert.False(t, list.HasMore, "the walk finished, so there is no next page to offer")
	assert.Empty(t, list.LastID, "and no cursor that would resume one")
}

// The single-page route is the one that does carry a cursor, because that is
// what --after resumes from.
func TestAPagedEvalListingKeepsTheServiceCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"data":     []map[string]any{{"id": "eval_1"}},
			"has_more": true,
			"last_id":  "eval_1",
		}))
	}))
	t.Cleanup(srv.Close)

	client := NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	list, err := client.ListOpenAIEvalsPage(t.Context(), 1, "")
	require.NoError(t, err)

	assert.True(t, list.HasMore)
	assert.Equal(t, "eval_1", list.LastID, "`eval list` hands this back as continuation_token")
}
