// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clientServing(t *testing.T, handler http.HandlerFunc) *EvalClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return NewEvalClientFromPipeline(srv.URL, pipeline)
}

// A listing that stops at the first page is a silent wrong answer rather than a
// short one. resolveEvalRef decides "is this name ambiguous?" from these rows,
// so a duplicate sitting on page two turns a refusal into a wrong choice, and
// `run start` then grades against a definition the caller did not mean.
func TestListOpenAIEvalsFollowsTheCursor(t *testing.T) {
	var afters []string
	c := clientServing(t, func(w http.ResponseWriter, r *http.Request) {
		after := r.URL.Query().Get("after")
		afters = append(afters, after)
		w.Header().Set("Content-Type", "application/json")
		switch after {
		case "":
			fmt.Fprint(w, `{"data":[{"id":"eval_1","name":"dup"},{"id":"eval_2","name":"other"}],`+
				`"has_more":true,"last_id":"eval_2"}`)
		default:
			fmt.Fprint(w, `{"data":[{"id":"eval_3","name":"dup"}],"has_more":false}`)
		}
	})

	list, err := c.ListOpenAIEvals(context.Background(), 0)

	require.NoError(t, err)
	require.Len(t, list.Data, 3, "both pages have to be gathered")
	assert.Equal(t, []string{"", "eval_2"}, afters,
		"the second request has to carry the cursor the first returned")

	var named int
	for _, e := range list.Data {
		if e.Name == "dup" {
			named++
		}
	}
	assert.Equal(t, 2, named, "the duplicate on page two is what makes the name ambiguous")
}

// The cursor is read only when the service sends one. Without this a service
// that omits it would loop forever or truncate, depending on the guard.
func TestListOpenAIEvalsStopsWithoutACursor(t *testing.T) {
	calls := 0
	c := clientServing(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"eval_1","name":"only"}]}`)
	})

	list, err := c.ListOpenAIEvals(context.Background(), 0)

	require.NoError(t, err)
	assert.Len(t, list.Data, 1)
	assert.Equal(t, 1, calls, "no cursor means one page, not an endless walk")
}

// has_more with no last_id is the other way a service can leave the walk
// without an anchor, and repeating the same request would never terminate.
func TestListOpenAIEvalsStopsWhenTheCursorIsEmpty(t *testing.T) {
	calls := 0
	c := clientServing(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"eval_1"}],"has_more":true,"last_id":""}`)
	})

	list, err := c.ListOpenAIEvals(context.Background(), 0)

	require.NoError(t, err)
	assert.Len(t, list.Data, 1)
	assert.Equal(t, 1, calls, "has_more without last_id has nowhere to go")
}

// An explicit limit still bounds the walk, and asks each page for only what is
// left rather than the whole limit again.
func TestListOpenAIEvalRunsHonoursTheLimit(t *testing.T) {
	var limits []string
	c := clientServing(t, func(w http.ResponseWriter, r *http.Request) {
		limits = append(limits, r.URL.Query().Get("limit"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"run_1"},{"id":"run_2"}],"has_more":true,"last_id":"run_2"}`)
	})

	list, err := c.ListOpenAIEvalRuns(context.Background(), "eval_1", 2)

	require.NoError(t, err)
	assert.Len(t, list.Data, 2, "the limit stops the walk")
	assert.Equal(t, []string{"2"}, limits, "one page satisfied it")
}

// run list without a limit has to report every run, not the newest page of
// them, or a run a caller started is missing from the list that should show it.
func TestListOpenAIEvalRunsGathersEveryPage(t *testing.T) {
	c := clientServing(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("after") {
		case "":
			fmt.Fprint(w, `{"data":[{"id":"run_1"},{"id":"run_2"}],"has_more":true,"last_id":"run_2"}`)
		case "run_2":
			fmt.Fprint(w, `{"data":[{"id":"run_3"}],"has_more":true,"last_id":"run_3"}`)
		default:
			fmt.Fprint(w, `{"data":[{"id":"run_4"}],"has_more":false}`)
		}
	})

	list, err := c.ListOpenAIEvalRuns(context.Background(), "eval_1", 0)

	require.NoError(t, err)
	assert.Len(t, list.Data, 4, "three pages, every run")
}
