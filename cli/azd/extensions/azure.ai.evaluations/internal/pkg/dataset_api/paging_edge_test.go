// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pagingEdgeClient(t *testing.T, h http.HandlerFunc) (*DatasetClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil)), srv
}

// A nextLink is allowed to be relative, and a relative one has no host or
// scheme of its own. Comparing it to the endpoint before resolving refused a
// legitimate link and turned a working listing into a hard failure.
func TestListDatasetsFollowsARelativeNextLink(t *testing.T) {
	c, _ := pagingEdgeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			fmt.Fprint(w, `{"value":[{"name":"one"}],"nextLink":"/datasets?page=2"}`)
			return
		}
		fmt.Fprint(w, `{"value":[{"name":"two"}]}`)
	})

	list, err := c.ListDatasets(t.Context(), testAPIVersion)
	require.NoError(t, err, "a relative nextLink must be followed, not refused")
	require.NotNil(t, list)
	require.Len(t, list.Value, 2)
	assert.Equal(t, "two", list.Value[1].Name)
}

// A relative link still has to stay on the endpoint. Resolving must not become
// a way to reach another host by writing a protocol-relative link.
func TestListDatasetsRefusesAProtocolRelativeLinkToAnotherHost(t *testing.T) {
	var elsewhereHits int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&elsewhereHits, 1)
		fmt.Fprint(w, `{"value":[{"name":"leaked"}]}`)
	}))
	t.Cleanup(elsewhere.Close)

	// "//host/path" resolves to the same scheme on a different host.
	c, _ := pagingEdgeClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"value":[{"name":"one"}],"nextLink":"//%s/datasets"}`,
			elsewhere.Listener.Addr().String())
	})

	_, err := c.ListDatasets(t.Context(), testAPIVersion)

	require.Error(t, err, "a link resolving to another host must be refused")
	assert.Zero(t, atomic.LoadInt32(&elsewhereHits), "the other host must never be contacted")
}

// A cycle longer than one hop used to run to maxPages, because only a link
// pointing at the page it came from ended the walk.
func TestListDatasetsStopsOnATwoPageCycle(t *testing.T) {
	var hits int32
	var base string
	c, srv := pagingEdgeClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		// a -> b -> a, which never repeats the immediately previous link.
		if r.URL.Query().Get("page") == "b" {
			fmt.Fprintf(w, `{"value":[{"name":"b"}],"nextLink":%q}`, base+"/datasets?page=a")
			return
		}
		fmt.Fprintf(w, `{"value":[{"name":"a"}],"nextLink":%q}`, base+"/datasets?page=b")
	})
	base = srv.URL

	list, err := c.ListDatasets(t.Context(), testAPIVersion)
	require.NoError(t, err, "a cycle ends the walk rather than failing the command")
	require.NotNil(t, list)
	assert.LessOrEqual(t, atomic.LoadInt32(&hits), int32(4),
		"a two-page cycle must stop quickly, not run to maxPages")
}
