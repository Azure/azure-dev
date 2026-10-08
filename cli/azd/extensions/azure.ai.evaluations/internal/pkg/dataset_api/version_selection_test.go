// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"fmt"
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

func versionDatasetDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "data.jsonl"), []byte(`{"query":"hi"}`+"\n"), 0o600))
	return dir
}

// An empty version listing is how a brand-new dataset looks, so a listing that
// failed must never be mistaken for one. Restarting at 1.0 against a dataset
// that already has versions either collides or publishes over the wrong one.
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

	_, err := c.UploadNextVersion(t.Context(), "ds", "", versionDatasetDir(t), testAPIVersion)

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
			w.WriteHeader(http.StatusInternalServerError) // stop here; the version is the point
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)

	c := NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	_, _ = c.UploadNextVersion(t.Context(), "ds", "", versionDatasetDir(t), testAPIVersion)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "1.0", startedVersion, "an unknown dataset still starts at 1.0")
}

func TestIsNotFoundOnlyMatchesA404(t *testing.T) {
	assert.False(t, IsNotFound(fmt.Errorf("plain error")), "a non-service error is not a 404")
	assert.False(t, IsNotFound(nil), "no error is not a 404")
}
