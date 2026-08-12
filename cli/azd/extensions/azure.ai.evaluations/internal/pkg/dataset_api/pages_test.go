// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

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

func datasetClientServing(t *testing.T, handler func(http.ResponseWriter, *http.Request, string)) *DatasetClient {
	t.Helper()
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, base)
	}))
	t.Cleanup(srv.Close)
	base = srv.URL

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return NewDatasetClientFromPipeline(srv.URL, pipeline)
}

// UploadVersion picks the next version from this listing, so a version sitting
// on page two meant reusing one that already exists.
func TestListDatasetVersionsFollowsNextLink(t *testing.T) {
	c := datasetClientServing(t, func(w http.ResponseWriter, r *http.Request, base string) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"value":[{"name":"golden","version":"3.0"}]}`)
			return
		}
		fmt.Fprintf(w, `{"value":[{"name":"golden","version":"1.0"},{"name":"golden","version":"2.0"}],`+
			`"nextLink":"%s/page?page=2"}`, base)
	})

	list, err := c.ListDatasetVersions(context.Background(), "golden", "v1")

	require.NoError(t, err)
	require.Len(t, list.Value, 3, "both pages have to be gathered")
	assert.Equal(t, "3.0", list.Value[2].Version, "the newest version was on page two")
}

// The link arrives in a response body and this client sends an Authorization
// header, so following one to another host would send the token there.
func TestNextLinkToAnotherHostIsRefused(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the client followed a link off its own host: %s", r.URL)
	}))
	t.Cleanup(elsewhere.Close)

	c := datasetClientServing(t, func(w http.ResponseWriter, r *http.Request, base string) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"value":[{"name":"golden","version":"1.0"}],"nextLink":"%s/steal"}`,
			elsewhere.URL)
	})

	_, err := c.ListDatasets(context.Background(), "v1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not followed")
}

// A link pointing at the page it came from is the one shape that would
// otherwise spin until the page bound for no benefit.
func TestSelfReferencingNextLinkStops(t *testing.T) {
	calls := 0
	c := datasetClientServing(t, func(w http.ResponseWriter, r *http.Request, base string) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"value":[{"name":"golden","version":"1.0"}],"nextLink":"%s/same"}`, base)
	})

	list, err := c.ListDatasets(context.Background(), "v1")

	require.NoError(t, err)
	assert.Equal(t, 2, calls, "the first request, then the link once")
	assert.Len(t, list.Value, 2)
}

// A listing without a link is one page, which is what this did before it could
// see the link at all.
func TestListingWithoutANextLinkIsOnePage(t *testing.T) {
	calls := 0
	c := datasetClientServing(t, func(w http.ResponseWriter, r *http.Request, base string) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"value":[{"name":"golden","version":"1.0"}]}`)
	})

	list, err := c.ListDatasets(context.Background(), "v1")

	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Len(t, list.Value, 1)
}
