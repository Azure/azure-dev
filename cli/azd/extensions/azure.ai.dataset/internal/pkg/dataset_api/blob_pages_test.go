// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func blobPage(marker string, names ...string) string {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><EnumerationResults><Blobs>`)
	for _, n := range names {
		fmt.Fprintf(&body, `<Blob><Name>%s</Name></Blob>`, n)
	}
	body.WriteString(`</Blobs><NextMarker>` + marker + `</NextMarker></EnumerationResults>`)
	return body.String()
}

// DownloadDatasetContent falls back to listing the container and taking the
// first .jsonl by name, so a container answered one page at a time could report
// no file, or a different one, depending on where the page happened to end.
func TestListContainerBlobsFollowsTheMarker(t *testing.T) {
	var markers []string

	client, srv := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		m := r.URL.Query().Get("marker")
		markers = append(markers, m)
		w.Header().Set("Content-Type", "application/xml")
		switch m {
		case "":
			fmt.Fprint(w, blobPage("m1", "a.jsonl", "b.jsonl"))
		case "m1":
			fmt.Fprint(w, blobPage("m2", "c.jsonl"))
		default:
			fmt.Fprint(w, blobPage("", "d.jsonl"))
		}
	})

	names, err := client.ListContainerBlobs(t.Context(), srv.URL+"/container?sig=redacted")

	require.NoError(t, err)
	assert.Equal(t, []string{"a.jsonl", "b.jsonl", "c.jsonl", "d.jsonl"}, names)
	assert.Equal(t, []string{"", "m1", "m2"}, markers,
		"each request has to carry the marker the previous page returned")
}

// An empty NextMarker is the last page, which is what this did before it could
// see the marker at all.
func TestListContainerBlobsStopsWithoutAMarker(t *testing.T) {
	calls := 0

	client, srv := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, blobPage("", "only.jsonl"))
	})

	names, err := client.ListContainerBlobs(t.Context(), srv.URL+"/container")

	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, []string{"only.jsonl"}, names)
}

// A marker that repeats itself would otherwise spin until the page bound.
//
// It stops, and it fails. The names this returns are what pickDatasetBlob
// chooses the .jsonl from, so a container whose data file sits on a page the
// walk never reached does not yield a shorter list -- it yields the wrong file,
// or "no dataset file" about a dataset that plainly has one.
func TestListContainerBlobsRefusesARepeatedMarker(t *testing.T) {
	calls := 0

	client, srv := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, blobPage("stuck", "same.jsonl"))
	})

	names, err := client.ListContainerBlobs(t.Context(), srv.URL+"/container")

	require.Error(t, err, "a stalled listing must not look like a complete one")
	assert.Nil(t, names, "the pages that did arrive are not the container")
	assert.Equal(t, 2, calls, "the first request, then the marker once")
}

// The other way a walk ends short: a service that keeps offering a fresh marker
// until the page cap runs out. Same partial answer, same refusal.
func TestListContainerBlobsRefusesToExhaustThePageCap(t *testing.T) {
	calls := 0

	client, srv := pagingClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, blobPage(fmt.Sprintf("m%d", calls), fmt.Sprintf("part-%d.jsonl", calls)))
	})

	names, err := client.ListContainerBlobs(t.Context(), srv.URL+"/container")

	require.Error(t, err, "exhausting the cap is still a partial answer")
	assert.Nil(t, names)
	assert.Equal(t, maxListPages, calls, "the cap has to bound the walk")
}
