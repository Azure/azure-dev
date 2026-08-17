// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"azureaidataset/internal/pkg/dataset_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// presenceServer stands up a fake project endpoint and records every path the
// presence probe asks for, so a test can assert what was tried as well as what
// was concluded.
type presenceServer struct {
	mu    sync.Mutex
	paths []string
}

// requested returns the paths seen so far, in order.
func (s *presenceServer) requested() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// newPresenceClient wires a DatasetClient to a server that answers the version
// listing with listStatus/listBody, and answers a point read of a dataset
// version with whatever found reports for that version.
//
// Retries are off: a test that means "the service said 404" should cost one
// request, not a backoff schedule.
func newPresenceClient(
	t *testing.T,
	listStatus int,
	listBody string,
	found map[string]bool,
) (*dataset_api.DatasetClient, *presenceServer) {
	t.Helper()

	rec := &presenceServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.paths = append(rec.paths, r.URL.Path)
		rec.mu.Unlock()

		// Assertions inside a handler run on the server's goroutine, where a
		// Fatalf would leave the client hanging on a response never written.
		assert.Equal(t, http.MethodGet, r.Method)

		switch {
		case strings.HasSuffix(r.URL.Path, "/versions"):
			w.WriteHeader(listStatus)
			_, _ = w.Write([]byte(listBody))
		default:
			version := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if found[version] {
				w.WriteHeader(http.StatusOK)
				// A fixed body: datasetPresence reads only whether the point
				// read succeeded, and echoing the request path back would make
				// this a taint sink for no benefit.
				_, _ = w.Write([]byte(`{"name":"ds"}`))
				return
			}
			http.Error(w, `{"error":{"code":"NotFound"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	client := dataset_api.NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))
	return client, rec
}

// TestPresenceTrustsANonEmptyVersionListing is the ordinary case: the listing
// answered, so no point read is needed.
func TestPresenceTrustsANonEmptyVersionListing(t *testing.T) {
	client, rec := newPresenceClient(t,
		http.StatusOK, `{"value":[{"name":"ds","version":"1.0"}]}`, nil)

	exists, absenceCertain := datasetPresence(t.Context(), client, "ds")

	require.True(t, exists)
	require.False(t, absenceCertain)
	require.Equal(t, []string{"/datasets/ds/versions"}, rec.requested(),
		"a listing that answered should settle it without a point read")
}

// TestPresenceProbesPastAListingThatHasNotCaughtUp covers the bug the probe
// exists for: a create publishes 1.0, the listing still reports nothing, and
// the update that follows must not be told the dataset is missing.
func TestPresenceProbesPastAListingThatHasNotCaughtUp(t *testing.T) {
	client, rec := newPresenceClient(t,
		http.StatusOK, `{"value":[]}`, map[string]bool{"1.0": true})

	exists, absenceCertain := datasetPresence(t.Context(), client, "ds")

	require.True(t, exists, "the point read found the version the listing had not")
	require.False(t, absenceCertain)
	require.Equal(t, []string{"/datasets/ds/versions", "/datasets/ds/versions/1.0"},
		rec.requested())
	require.NoError(t, checkAssetExistence("update", "dataset", "ds", exists, absenceCertain))
	require.Error(t, checkAssetExistence("create", "dataset", "ds", exists, absenceCertain),
		"create must still refuse a name the probe found")
}

// TestPresenceProbesTheVersionSomethingElseRegistered covers a dataset created
// by the portal, the SDK or a generation job, which numbers its first version
// "1" rather than the "1.0" this CLI publishes.
func TestPresenceProbesTheVersionSomethingElseRegistered(t *testing.T) {
	client, rec := newPresenceClient(t,
		http.StatusOK, `{"value":[]}`, map[string]bool{"1": true})

	exists, absenceCertain := datasetPresence(t.Context(), client, "ds")

	require.True(t, exists)
	require.False(t, absenceCertain)
	require.Equal(t,
		[]string{"/datasets/ds/versions", "/datasets/ds/versions/1.0", "/datasets/ds/versions/1"},
		rec.requested(),
		"both first-publish versions should be probed before giving up")
}

// TestPresenceWillNotCallAnEmptyListingProofOfAbsence is the guard that keeps
// `update` working against a service whose listing lags. An empty 200 is not a
// 404, so the gate must let the update through rather than refuse it.
func TestPresenceWillNotCallAnEmptyListingProofOfAbsence(t *testing.T) {
	client, _ := newPresenceClient(t, http.StatusOK, `{"value":[]}`, nil)

	exists, absenceCertain := datasetPresence(t.Context(), client, "ds")

	require.False(t, exists)
	require.False(t, absenceCertain,
		"an empty listing does not distinguish an unknown dataset from a stale one")
	require.NoError(t, checkAssetExistence("update", "dataset", "ds", exists, absenceCertain),
		"update must proceed when absence is unproven")
}

// TestPresenceTreatsA404ListingAsProofOfAbsence is the other half: a service
// that actually said "no such dataset" should stop an update before it uploads.
func TestPresenceTreatsA404ListingAsProofOfAbsence(t *testing.T) {
	client, _ := newPresenceClient(t,
		http.StatusNotFound, `{"error":{"code":"NotFound"}}`, nil)

	exists, absenceCertain := datasetPresence(t.Context(), client, "ds")

	require.False(t, exists)
	require.True(t, absenceCertain)

	err := checkAssetExistence("update", "dataset", "ds", exists, absenceCertain)
	require.Error(t, err)
	require.Contains(t, err.Error(), `dataset "ds" does not exist`)
	require.NoError(t, checkAssetExistence("create", "dataset", "ds", exists, absenceCertain),
		"create is exactly what a proven-absent name should allow")
}
