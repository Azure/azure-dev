// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func blobPage(marker string, names ...string) string {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><EnumerationResults><Blobs>`)
	for _, n := range names {
		body.WriteString(fmt.Sprintf(`<Blob><Name>%s</Name></Blob>`, n))
	}
	body.WriteString(`</Blobs><NextMarker>` + marker + `</NextMarker></EnumerationResults>`)
	return body.String()
}

// DownloadDatasetContent falls back to listing the container and taking the
// first .jsonl by name, so a container answered one page at a time could report
// no file, or a different one, depending on where the page happened to end.
func TestListContainerBlobsFollowsTheMarker(t *testing.T) {
	var markers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	c := NewDatasetClientFromPipeline(srv.URL, pipeline)

	names, err := c.ListContainerBlobs(context.Background(), srv.URL+"/container?sig=redacted")

	require.NoError(t, err)
	assert.Equal(t, []string{"a.jsonl", "b.jsonl", "c.jsonl", "d.jsonl"}, names)
	assert.Equal(t, []string{"", "m1", "m2"}, markers,
		"each request has to carry the marker the previous page returned")
}

// An empty NextMarker is the last page, which is what this did before it could
// see the marker at all.
func TestListContainerBlobsStopsWithoutAMarker(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, blobPage("", "only.jsonl"))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	c := NewDatasetClientFromPipeline(srv.URL, pipeline)

	names, err := c.ListContainerBlobs(context.Background(), srv.URL+"/container")

	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, []string{"only.jsonl"}, names)
}

// A marker that repeats itself would otherwise spin until the page bound.
func TestListContainerBlobsStopsOnARepeatedMarker(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, blobPage("stuck", "same.jsonl"))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	c := NewDatasetClientFromPipeline(srv.URL, pipeline)

	_, err := c.ListContainerBlobs(context.Background(), srv.URL+"/container")

	require.NoError(t, err)
	assert.Equal(t, 2, calls, "the first request, then the marker once")
}
