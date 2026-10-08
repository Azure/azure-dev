// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"azureaidataset/internal/pkg/dataset_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	emptyListing     = `{"value":[]}`
	listingWithSeven = `{"value":[{"name":"ds","version":"7.0"}]}`
)

// newSettleClient answers the version listing from answer, which is handed the
// number of the call so a test can describe a listing that catches up part way
// through.
func newSettleClient(
	t *testing.T,
	answer func(call int) (int, string),
) (*dataset_api.DatasetClient, func() int) {
	t.Helper()

	var mu sync.Mutex
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()

		status, body := answer(n)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	client := dataset_api.NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))
	return client, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// The bug this exists for: `create ds --version 7.0` leaves a listing that has
// not caught up, and the presence probe cannot see 7.0 because it only knows
// the versions a first publish can carry. Counting from that empty answer
// publishes 1.0 and runs the sequence backwards.
func TestUpdateWaitsForTheVersionListingToCatchUp(t *testing.T) {
	client, calls := newSettleClient(t, func(call int) (int, string) {
		if call < 3 {
			return http.StatusOK, emptyListing
		}
		return http.StatusOK, listingWithSeven
	})

	latest, err := settledLatestVersion(t.Context(), client, "ds")

	require.NoError(t, err)
	assert.Equal(t, "7.0", latest,
		"the listing caught up, so the next version counts from 7.0 rather than nothing")
	assert.Equal(t, 3, calls(), "it stops asking as soon as the listing answers")
}

// Waiting narrows the window; it does not close it. A listing that is still
// empty when the attempts run out is believed, because that is also what a
// dataset with no versions looks like.
func TestASettledEmptyListingIsBelieved(t *testing.T) {
	client, calls := newSettleClient(t, func(int) (int, string) {
		return http.StatusOK, emptyListing
	})

	latest, err := settledLatestVersion(t.Context(), client, "ds")

	require.NoError(t, err)
	assert.Empty(t, latest, "nothing to count from is not an error")
	assert.Equal(t, versionListingSettleAttempts, calls(), "the wait has to be bounded")
}

// A read that failed proves nothing about the versions, and must not be spent
// as evidence that there are none: that restarts an existing dataset at 1.0.
func TestASettleReadThatFailedIsNotNoVersions(t *testing.T) {
	client, _ := newSettleClient(t, func(int) (int, string) {
		return http.StatusForbidden, `{"error":{"code":"AuthorizationFailed"}}`
	})

	_, err := settledLatestVersion(t.Context(), client, "ds")

	require.Error(t, err, "a refused listing must surface rather than read as empty")
}

// A 404 is the service naming the dataset unknown, which is an answer rather
// than a lag, so there is nothing to wait for.
func TestASettleStopsWhenTheServiceSaysUnknown(t *testing.T) {
	client, calls := newSettleClient(t, func(int) (int, string) {
		return http.StatusNotFound, `{"error":{"code":"ResourceNotFound"}}`
	})

	latest, err := settledLatestVersion(t.Context(), client, "ds")

	require.NoError(t, err)
	assert.Empty(t, latest)
	assert.Equal(t, 1, calls(), "an unknown dataset is not worth waiting on")
}
