// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Generation-job listings answer with has_more and last_id -- an ordinary row
// id, not an opaque continuation URL -- so a page can say where the next one
// starts and the caller can hand that back on a later invocation.
//
// The listing used to walk every page and trim what it had gathered, which read
// the whole history to show twenty rows and left `-o json` callers no way to
// ask for the twenty-first.
func TestAJobPageAsksForOnePageAndReportsWhereToResume(t *testing.T) {
	t.Parallel()

	var gotLimit, gotAfter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		gotAfter = r.URL.Query().Get("after")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"data":[{"id":"job_2","status":"succeeded"}],"has_more":true,"last_id":"job_2"}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	page, err := client.ListDataGenerationJobsPage(t.Context(), 20, "job_1", "2024-01-01")

	require.NoError(t, err)
	assert.Equal(t, "20", gotLimit, "the page size has to reach the service")
	assert.Equal(t, "job_1", gotAfter, "the cursor has to reach the service")
	require.Len(t, page.Data, 1)
	assert.True(t, page.HasMore, "has_more survives to the caller")
	assert.Equal(t, "job_2", page.LastID, "last_id is the cursor for the next page")
}

// One page only: the walk that gathers every page is what --all asks for, and
// the paged call must not quietly do it too.
func TestAJobPageDoesNotWalkPastTheFirst(t *testing.T) {
	t.Parallel()

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"data":[{"id":"job_1","status":"running"}],"has_more":true,"last_id":"job_1"}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	_, err := client.ListEvaluatorGenerationJobsPage(t.Context(), 10, "", "2024-01-01")

	require.NoError(t, err)
	assert.Equal(t, 1, requests, "a page is one request, however much the service says is left")
}

// Omitted paging means omitted parameters rather than zero values, which the
// service would read as a request for no rows.
func TestAJobPageWithoutPagingSendsNeitherParameter(t *testing.T) {
	t.Parallel()

	var hadLimit, hadAfter bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadLimit = r.URL.Query()["limit"]
		_, hadAfter = r.URL.Query()["after"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"has_more":false,"last_id":""}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	_, err := client.ListDataGenerationJobsPage(t.Context(), 0, "", "2024-01-01")

	require.NoError(t, err)
	assert.False(t, hadLimit, "no limit asked for means none sent")
	assert.False(t, hadAfter, "no cursor means none sent")
}
