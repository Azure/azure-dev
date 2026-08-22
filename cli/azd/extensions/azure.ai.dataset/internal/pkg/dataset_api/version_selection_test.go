// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// datasetDir returns a directory holding one uploadable dataset file, so the
// upload path is exercised rather than short-circuiting on an empty folder.
func datasetDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "data.jsonl"), []byte(`{"query":"hi"}`+"\n"), 0o600))
	return dir
}

// An empty version listing is how a brand-new dataset looks, so a listing that
// failed must never be mistaken for one. Restarting at 1.0 against a dataset
// that already has versions is the damaging case: the upload either collides or
// publishes over the wrong version.
func TestUploadNextVersionRefusesToStartOverWhenTheListingFails(t *testing.T) {
	var mu sync.Mutex
	var paths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()

		if strings.HasSuffix(r.URL.Path, "/versions") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"AuthorizationFailed"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c := NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	_, err := c.UploadNextVersion(t.Context(), "ds", "", datasetDir(t), testAPIVersion)

	require.Error(t, err, "a refused listing must surface, not read as a new dataset")

	mu.Lock()
	defer mu.Unlock()
	for _, p := range paths {
		assert.NotContains(t, p, "startPendingUpload",
			"no upload may be attempted once the version listing failed")
	}
}

// A 404 is the service saying the dataset does not exist, which genuinely means
// "no versions yet" and must stay distinguishable from a failure.
func TestUploadNextVersionTreatsAnUnknownDatasetAsVersionless(t *testing.T) {
	var mu sync.Mutex
	var startedVersion string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"ResourceNotFound"}}`))
		case strings.HasSuffix(r.URL.Path, "/startPendingUpload"):
			mu.Lock()
			v := strings.TrimSuffix(r.URL.Path, "/startPendingUpload")
			startedVersion = v[strings.LastIndex(v, "/")+1:]
			mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError) // stop the flow here; the version is the point
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)

	c := NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	_, _ = c.UploadNextVersion(t.Context(), "ds", "", datasetDir(t), testAPIVersion)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "1.0", startedVersion, "an unknown dataset still starts at 1.0")
}

// LatestVersion documents a fallback to the last entry when nothing can be
// ordered. That fallback only runs if an unorderable version never becomes the
// running best.
func TestLatestVersionFallsBackToTheLastEntryWhenNoneAreOrderable(t *testing.T) {
	got := LatestVersion([]Dataset{{Version: "alpha"}, {Version: "beta"}, {Version: "gamma"}})
	assert.Equal(t, "gamma", got, "with nothing orderable the service's last entry wins")
}

func TestLatestVersionPrefersAnOrderableVersionOverAnUnorderableOne(t *testing.T) {
	assert.Equal(t, "2.0", LatestVersion([]Dataset{{Version: "alpha"}, {Version: "2.0"}}))
	assert.Equal(t, "2.0", LatestVersion([]Dataset{{Version: "2.0"}, {Version: "alpha"}}))
}
