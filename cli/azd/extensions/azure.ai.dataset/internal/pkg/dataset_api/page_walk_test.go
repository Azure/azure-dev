// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A 404 on the first page means the service does not know this dataset. A 404
// on a later page means the continuation failed -- the first page already
// proved the dataset exists. Reading the second as the first answered "no
// versions, no error", which restarts an existing dataset at 1.0.
func TestALaterPageFailingIsNotAbsence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			http.Error(w, `{"error":{"code":"NotFound"}}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(
			`{"value":[{"name":"ds","version":"3.0"}],"nextLink":"` +
				"http://" + r.Host + `/datasets/ds/versions?page=2"}`))
	}))
	t.Cleanup(srv.Close)

	client := NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	_, err := client.ListDatasetVersions(t.Context(), "ds", "2025-11-15-preview")
	require.Error(t, err)
	assert.False(t, IsNotFound(err),
		"the first page proved the dataset exists; a later 404 is the walk failing")

	version, err := client.latestRegisteredVersion(t.Context(), "ds", "2025-11-15-preview")
	require.Error(t, err, "a failed walk must not answer with a version")
	assert.Empty(t, version)
	assert.Contains(t, strings.ToLower(err.Error()), "page")
}

// The first page answering 404 is still absence, which is what lets a create
// know the name is free.
func TestAFirstPage404IsStillAbsence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"NotFound"}}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	client := NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	_, err := client.ListDatasetVersions(t.Context(), "ds", "2025-11-15-preview")
	require.Error(t, err)
	assert.True(t, IsNotFound(err))

	version, err := client.latestRegisteredVersion(t.Context(), "ds", "2025-11-15-preview")
	require.NoError(t, err, "an unknown dataset has no versions and that is not a failure")
	assert.Empty(t, version)
}
