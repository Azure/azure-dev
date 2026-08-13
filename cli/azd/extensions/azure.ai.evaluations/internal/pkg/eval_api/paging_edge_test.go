// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clientAndServer is clientServing plus the server, for tests that need to
// build an absolute link back to it.
func clientAndServer(t *testing.T, handler http.HandlerFunc) (*EvalClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return NewEvalClientFromPipeline(srv.URL, pipeline), srv
}

// A nextLink is allowed to be relative, and a relative one has no host or
// scheme of its own. Comparing it to the endpoint before resolving refused a
// legitimate link, which turned a working listing into a hard failure.
func TestListEvaluatorVersionsFollowsARelativeNextLink(t *testing.T) {
	c, _ := clientAndServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			fmt.Fprint(w, `{"value":[{"name":"one"}],"nextLink":"/evaluators/e/versions?page=2"}`)
			return
		}
		fmt.Fprint(w, `{"value":[{"name":"two"}]}`)
	})

	list, err := c.ListEvaluatorVersions(t.Context(), "e", "v1")
	require.NoError(t, err, "a relative nextLink must be followed, not refused")
	require.NotNil(t, list)
	require.Len(t, list.Value, 2, "the second page has to be merged in")
}

// Resolving relative links must not become a way to reach another host: a
// protocol-relative link keeps the scheme and swaps the host, and this client
// sends an Authorization header.
func TestListEvaluatorVersionsRefusesALinkResolvingToAnotherHost(t *testing.T) {
	var elsewhereHits int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&elsewhereHits, 1)
		fmt.Fprint(w, `{"value":[{"name":"leaked"}]}`)
	}))
	t.Cleanup(elsewhere.Close)

	c, _ := clientAndServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"value":[{"name":"one"}],"nextLink":"//%s/evaluators"}`,
			elsewhere.Listener.Addr().String())
	})

	_, err := c.ListEvaluatorVersions(t.Context(), "e", "v1")

	require.Error(t, err, "a link resolving to another host must be refused")
	assert.Zero(t, atomic.LoadInt32(&elsewhereHits),
		"the token must never be sent to the other host")
}

// A cycle longer than one hop used to run to maxPages, because only a link
// pointing at the page it came from ended the walk.
func TestListEvaluatorVersionsStopsOnATwoPageCycle(t *testing.T) {
	var hits int32
	var base string
	c, srv := clientAndServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		// a -> b -> a, so no link ever repeats the one immediately before it.
		if r.URL.Query().Get("page") == "b" {
			fmt.Fprintf(w, `{"value":[{"name":"b"}],"nextLink":%q}`, base+"/evaluators?page=a")
			return
		}
		fmt.Fprintf(w, `{"value":[{"name":"a"}],"nextLink":%q}`, base+"/evaluators?page=b")
	})
	base = srv.URL

	list, err := c.ListEvaluatorVersions(t.Context(), "e", "v1")
	require.NoError(t, err, "a cycle ends the walk rather than failing the command")
	require.NotNil(t, list)
	assert.LessOrEqual(t, atomic.LoadInt32(&hits), int32(4),
		"a two-page cycle must stop quickly, not run to maxPages")
}
