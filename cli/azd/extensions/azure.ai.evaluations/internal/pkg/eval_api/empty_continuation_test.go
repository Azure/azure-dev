// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A nextLink that answers with nothing is the walk breaking, not its end.
//
// The page before it named that link, so the service said there was more and
// then sent none. Ending the walk there returned a short listing no caller
// could tell from a complete one -- and these rows settle which evaluator
// version is latest and whether a name is ambiguous, so a publish would pick
// its baseline from the pages that happened to arrive.
func TestAnEmptyContinuationPageBreaksTheWalk(t *testing.T) {
	var base string
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			fmt.Fprintf(w, `{"value":[{"name":"kept","version":"1"}],"nextLink":%q}`,
				base+"/evaluators?page=2")
			return
		}
		w.WriteHeader(http.StatusOK) // no body, after the service promised one
	}))
	t.Cleanup(srv.Close)
	base = srv.URL

	client := NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	list, err := client.ListEvaluatorVersions(t.Context(), "kept", "2024-01-01")

	require.Error(t, err, "the service offered another page and then sent none")
	assert.Contains(t, err.Error(), "incomplete")
	assert.Nil(t, list, "a partial listing must not reach the caller as an answer")
	assert.False(t, IsNotFound(err),
		"the first page answered, so this is the walk failing and not an unknown evaluator")
}

// And the version resolver built on it refuses rather than answering zero,
// which is what would let a version the service already holds be republished.
func TestLatestVersionRefusesABrokenWalk(t *testing.T) {
	var base string
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			body, _ := json.Marshal(map[string]any{
				"value":    []map[string]any{{"name": "kept", "version": "3"}},
				"nextLink": base + "/evaluators?page=2",
			})
			_, _ = w.Write(body)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	base = srv.URL

	client := NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	got, err := client.LatestEvaluatorVersionNumber(t.Context(), "kept", "2024-01-01")

	require.Error(t, err, "a listing that broke is not an evaluator with no versions")
	assert.Zero(t, got)
}
