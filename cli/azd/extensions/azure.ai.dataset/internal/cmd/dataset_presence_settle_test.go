// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"azureaidataset/internal/pkg/dataset_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// quickSettle drops the retry delay so a test pins the number of attempts
// rather than the wall clock.
func quickSettle(t *testing.T) {
	t.Helper()
	original := versionListingSettleDelay
	versionListingSettleDelay = time.Millisecond
	t.Cleanup(func() { versionListingSettleDelay = original })
}

// laggingListingClient answers the version listing with an empty 200 until it
// has been asked emptyFor times, then reports one version at `version`. Point
// reads of any version always 404, which is what a dataset first published at
// an explicit version looks like to a probe that only knows 1 and 1.0.
func laggingListingClient(
	t *testing.T, emptyFor int32, version string,
) (*dataset_api.DatasetClient, *int32) {
	t.Helper()

	var listCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/versions") {
			http.Error(w, `{"error":{"code":"NotFound"}}`, http.StatusNotFound)
			return
		}
		seen := atomic.AddInt32(&listCalls, 1)
		w.WriteHeader(http.StatusOK)
		if seen > emptyFor {
			_, _ = w.Write([]byte(`{"value":[{"name":"ds","version":"` + version + `"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"value":[]}`))
	}))
	t.Cleanup(srv.Close)

	client := dataset_api.NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))
	return client, &listCalls
}

// A dataset first registered at an explicit version -- `create --version 7.0`
// -- is invisible to the point probes, which only know the versions a first
// publish can carry. With the listing also behind, presence reported "not
// there but we cannot prove it", `create` was allowed, and it published 1.0
// over a dataset that already existed: the one promise create makes, broken by
// a lag nobody waited out.
func TestPresenceWaitsOutALaggingListingBeforeLettingCreateThrough(t *testing.T) {
	quickSettle(t)
	client, listCalls := laggingListingClient(t, 2, "7.0")

	exists, absenceCertain, err := datasetPresence(t.Context(), client, "ds")
	require.NoError(t, err)

	assert.True(t, exists, "the listing caught up and named a version")
	assert.False(t, absenceCertain)
	assert.Error(t, checkAssetExistence("create", "dataset", "ds", exists, absenceCertain),
		"create must refuse a dataset that is already registered")
	assert.Greater(t, atomic.LoadInt32(listCalls), int32(1),
		"the first empty answer has to be retried, not believed")
}

// The retry is bounded. A listing that never names anything is taken at face
// value in the end, or `create` of a genuinely new dataset would never run.
func TestPresenceStillAllowsCreateWhenTheListingNeverCatchesUp(t *testing.T) {
	quickSettle(t)
	client, listCalls := laggingListingClient(t, 1000, "7.0")

	exists, absenceCertain, err := datasetPresence(t.Context(), client, "ds")
	require.NoError(t, err)

	assert.False(t, exists)
	assert.False(t, absenceCertain, "an empty listing never proved absence and still does not")
	assert.NoError(t, checkAssetExistence("create", "dataset", "ds", exists, absenceCertain))
	assert.LessOrEqual(t, int(atomic.LoadInt32(listCalls)), versionListingSettleAttempts+1,
		"the wait is bounded")
}

// The ordinary path must not pay for the retry: an unknown name answers 404,
// which is an answer, and nothing is retried.
func TestPresenceDoesNotRetryAListingThatAnswered404(t *testing.T) {
	quickSettle(t)
	client, rec := newPresenceClient(t, http.StatusNotFound, `{"error":{"code":"NotFound"}}`, nil)

	exists, absenceCertain, err := datasetPresence(t.Context(), client, "ds")
	require.NoError(t, err)

	assert.False(t, exists)
	assert.True(t, absenceCertain, "404 is the service naming it unknown")

	listings := 0
	for _, p := range rec.requested() {
		if strings.HasSuffix(p, "/versions") {
			listings++
		}
	}
	assert.Equal(t, 1, listings, "a 404 is an answer, so it is asked once")
}
