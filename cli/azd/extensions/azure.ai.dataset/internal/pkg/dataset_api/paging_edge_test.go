// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A nextLink is allowed to be relative. Rejecting one for having no scheme or
// host of its own would fail a legitimate listing, so it is resolved against
// the endpoint before the origin check runs.
func TestListDatasetsFollowsARelativeNextLink(t *testing.T) {
	c, _ := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
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

// An empty 200 ends the walk. Unmarshaling it would fail and throw away every
// page already collected.
func TestListDatasetsKeepsEarlierPagesWhenAPageComesBackEmpty(t *testing.T) {
	var base string
	client, srv := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			fmt.Fprintf(w, `{"value":[{"name":"kept"}],"nextLink":%q}`, base+"/datasets?page=2")
			return
		}
		w.WriteHeader(http.StatusOK) // no body
	})
	base = srv.URL

	list, err := client.ListDatasets(t.Context(), testAPIVersion)
	require.NoError(t, err, "an empty page ends the walk rather than failing it")
	require.NotNil(t, list)
	require.Len(t, list.Value, 1)
	assert.Equal(t, "kept", list.Value[0].Name)
}

// followPages must not append into the first page's backing array, which the
// caller still owns.
func TestFollowPagesDoesNotWriteIntoTheCallersSlice(t *testing.T) {
	backing := make([]Dataset, 1, 4)
	backing[0] = Dataset{Name: "first"}
	first := &DatasetList{Value: backing}

	var hits int32
	var base string
	client, srv := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		fmt.Fprint(w, `{"value":[{"name":"second"}]}`)
	})
	base = srv.URL
	first.NextLink = base + "/datasets?page=2"

	out, err := client.followPages(t.Context(), first)
	require.NoError(t, err)
	require.Len(t, out.Value, 2)
	assert.Equal(t, "first", backing[0].Name)
	assert.Equal(t, 1, len(first.Value), "the caller's slice keeps its own length")
}
