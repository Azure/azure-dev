// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Resolving a relative nextLink against the endpoint is what makes a
// protocol-relative link dangerous: "//host/path" has no scheme of its own, so
// it inherits the endpoint's and resolves to a different host entirely. The
// pipeline attaches the caller's token, so that host must never be contacted.
func TestListDatasetsRefusesAProtocolRelativeNextLink(t *testing.T) {
	var elsewhereHits int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&elsewhereHits, 1)
		fmt.Fprint(w, `{"value":[{"name":"leaked"}]}`)
	}))
	t.Cleanup(elsewhere.Close)

	// elsewhere.URL is http://127.0.0.1:PORT; strip the scheme to make it
	// protocol-relative, which is the form that inherits ours.
	hostOnly := elsewhere.URL[len("http:"):]

	client, _ := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"value":[{"name":"one"}],"nextLink":%q}`, hostOnly+"/datasets")
	})

	_, err := client.ListDatasets(t.Context(), testAPIVersion)

	require.Error(t, err, "a protocol-relative link to another host must be refused")
	assert.Zero(t, atomic.LoadInt32(&elsewhereHits),
		"the other host must never receive a request carrying our token")
}

// A guard that only remembers the previous link lets a two-hop cycle through.
// This one alternates A and B forever, so it hangs rather than merely running
// long if the walk does not remember every link it has followed.
func TestListDatasetsStopsOnATwoHopCycle(t *testing.T) {
	var base string
	var hits int32

	client, srv := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		next := "a"
		if r.URL.Query().Get("page") == "a" {
			next = "b"
		}
		fmt.Fprintf(w, `{"value":[{"name":"loop"}],"nextLink":%q}`, base+"/datasets?page="+next)
	})
	base = srv.URL

	list, err := client.ListDatasets(t.Context(), testAPIVersion)
	require.NoError(t, err, "a cycle ends the walk rather than failing the command")
	require.NotNil(t, list)
	assert.LessOrEqual(t, atomic.LoadInt32(&hits), int32(4),
		"A to B to A must terminate, not alternate forever")
}
