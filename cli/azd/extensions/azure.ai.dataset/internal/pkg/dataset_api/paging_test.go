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

func pagingClient(t *testing.T, h http.HandlerFunc) (*DatasetClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil)), srv
}

// A project with more datasets than fit in one page must list completely;
// stopping at page one silently hides datasets and lets a latest-version check
// decide from a stale prefix.
func TestListDatasetsFollowsNextLinkAcrossPages(t *testing.T) {
	var srvURL string
	c, srv := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "":
			fmt.Fprintf(w, `{"value":[{"name":"one"}],"nextLink":%q}`, srvURL+"/datasets?page=2")
		case "2":
			fmt.Fprintf(w, `{"value":[{"name":"two"}],"nextLink":%q}`, srvURL+"/datasets?page=3")
		default:
			fmt.Fprint(w, `{"value":[{"name":"three"}]}`)
		}
	})
	srvURL = srv.URL

	list, err := c.ListDatasets(t.Context(), testAPIVersion)
	require.NoError(t, err)
	require.NotNil(t, list)

	var names []string
	for _, d := range list.Value {
		names = append(names, d.Name)
	}
	assert.Equal(t, []string{"one", "two", "three"}, names, "every page contributes")
}

// nextLink is service-supplied and the pipeline attaches the caller's token, so
// a link off the project's origin must be refused rather than followed.
func TestListDatasetsRefusesANextLinkOffTheEndpointOrigin(t *testing.T) {
	var elsewhereHits int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&elsewhereHits, 1)
		fmt.Fprint(w, `{"value":[{"name":"leaked"}]}`)
	}))
	t.Cleanup(elsewhere.Close)

	c, _ := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"value":[{"name":"one"}],"nextLink":%q}`, elsewhere.URL+"/datasets")
	})

	_, err := c.ListDatasets(t.Context(), testAPIVersion)

	require.Error(t, err, "an off-origin nextLink must fail the call, not be followed")
	assert.Contains(t, err.Error(), "not the project endpoint")
	assert.Zero(t, atomic.LoadInt32(&elsewhereHits), "the other host must never be contacted")
}

// A service that keeps returning the same link must not spin forever.
func TestListDatasetsStopsOnARepeatedNextLink(t *testing.T) {
	var srvURL string
	var hits int32
	c, srv := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		fmt.Fprintf(w, `{"value":[{"name":"loop"}],"nextLink":%q}`, srvURL+"/datasets?page=same")
	})
	srvURL = srv.URL

	list, err := c.ListDatasets(t.Context(), testAPIVersion)
	require.NoError(t, err)
	require.NotNil(t, list)
	assert.LessOrEqual(t, atomic.LoadInt32(&hits), int32(3), "the repeated link is followed at most once")
}
