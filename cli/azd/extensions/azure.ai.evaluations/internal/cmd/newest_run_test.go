// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The service promises no order on run listings, so the newest run can sit
// anywhere in them. Reading a fixed prefix therefore could not answer "which
// run is newest": with more runs than the old 50-row cap, the commands that
// fall back to the latest run settled on whichever row came back first, and
// `run cancel` acted on it.
//
// The listing here puts the newest run last, past the old cap, so a capped
// read cannot find it.
func TestNewestRunIsFoundBeyondTheOldCandidateCap(t *testing.T) {
	const total = 120
	const newestID = "run-newest"

	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		after := r.URL.Query().Get("after")

		start := 0
		if after != "" {
			_, err := fmt.Sscanf(after, "run-%d", &start)
			require.NoError(t, err)
			start++
		}
		end := min(start+25, total)

		runs := make([]map[string]any, 0, end-start)
		for i := start; i < end; i++ {
			id := fmt.Sprintf("run-%d", i)
			// Every row shares one early timestamp so only the last row's
			// later one can win; position alone must not decide it.
			runs = append(runs, map[string]any{
				"id": id, "status": "completed", "created_at": "2020-01-01T00:00:00Z",
			})
		}
		last := fmt.Sprintf("run-%d", end-1)
		if end == total {
			runs = append(runs, map[string]any{
				"id": newestID, "status": "completed", "created_at": "2030-01-01T00:00:00Z",
			})
			last = newestID
		}

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"data": runs, "has_more": end < total, "last_id": last,
		}))
	}))
	defer srv.Close()

	client := eval_api.NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	// The read the old code did: a 50-row cap over an unordered listing.
	capped, err := client.ListOpenAIEvalRuns(t.Context(), "eval-1", 50)
	require.NoError(t, err)
	assert.NotEqual(t, newestID, newestRunIn(capped.Data).ID,
		"if a capped read can find it this test proves nothing")

	list, err := client.ListOpenAIEvalRuns(t.Context(), "eval-1", 0)
	require.NoError(t, err)
	assert.Greater(t, pages, 1, "the listing has to be walked, not read once")
	assert.Len(t, list.Data, total+1)

	newest := newestRunIn(list.Data)
	assert.Equal(t, newestID, newest.ID,
		"the newest run sits past the old cap, so a capped read would miss it")
}
