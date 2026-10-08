// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"encoding/json"
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

// uploadServer answers the three-step publish, refusing any version in taken
// and reporting whatever the listing is told to report.
type uploadServer struct {
	mu       sync.Mutex
	taken    map[string]bool
	listing  []string
	attempts []string
}

func (s *uploadServer) handler(t *testing.T, base func() string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/startPendingUpload"):
			version := strings.Split(r.URL.Path, "/versions/")[1]
			version = strings.TrimSuffix(version, "/startPendingUpload")
			s.attempts = append(s.attempts, version)
			if s.taken[version] {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":{"code":"Conflict"}}`))
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"blobReference": map[string]any{
					"blobUri":             base() + "/c",
					"storageAccountArmId": "id",
					"credential":          map[string]any{"sasUri": base() + "/c?sig=x"},
				},
			}))

		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/versions"):
			values := []map[string]any{}
			for _, v := range s.listing {
				values = append(values, map[string]any{"name": "ds", "version": v})
			}
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"value": values}))

		case r.Method == http.MethodPut:
			version := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			s.taken[version] = true
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"name": "ds", "version": version,
			}))

		default:
			// The blob PUT.
			w.WriteHeader(http.StatusCreated)
		}
	}
}

// The version listing lags a publish, so a second upload can be told the
// dataset is new and restart at a version that already exists. Trusting the
// listing alone surfaced that 409 to the user for a publish that should simply
// have added a version.
func TestUploadNextVersionWalksPastAStaleListing(t *testing.T) {
	server := &uploadServer{taken: map[string]bool{"1.0": true}}
	// The listing has not caught up: it still reports nothing at all.
	httpServer := func() *httptest.Server {
		var s *httptest.Server
		s = httptest.NewServer(server.handler(t, func() string { return s.URL }))
		return s
	}()
	t.Cleanup(httpServer.Close)

	client := NewDatasetClientFromPipeline(
		httpServer.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "rows.jsonl"), []byte("{\"query\":\"q\"}\n"), 0o600))

	ds, err := client.UploadNextVersion(context.Background(), "ds", "", dir, "2025-11-15-preview")
	require.NoError(t, err, "a stale listing must not surface as a conflict")
	assert.Equal(t, "2.0", ds.Version)
	assert.Equal(t, []string{"1.0", "2.0"}, server.attempts,
		"the version just refused is proof it exists, so the next one is tried")
}

// A declared version is the version published, not one to count from.
//
// `--version` used to reach the incrementing path, so `--version 7.0` published
// 8.0 while `version: 7.0` in configuration published 7.0 -- one word, two
// answers, decided by where it was written.
func TestUploadVersionPublishesTheVersionDeclared(t *testing.T) {
	server := &uploadServer{taken: map[string]bool{}, listing: []string{"1.0", "2.0"}}
	httpServer := func() *httptest.Server {
		var s *httptest.Server
		s = httptest.NewServer(server.handler(t, func() string { return s.URL }))
		return s
	}()
	t.Cleanup(httpServer.Close)

	client := NewDatasetClientFromPipeline(
		httpServer.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "rows.jsonl"), []byte("{\"query\":\"q\"}\n"), 0o600))

	ds, err := client.UploadVersion(context.Background(), "ds", "7.0", dir, "2025-11-15-preview")
	require.NoError(t, err)
	assert.Equal(t, "7.0", ds.Version, "the version asked for is the version written")
	assert.Equal(t, []string{"7.0"}, server.attempts,
		"a declared version is published as given, not counted from")
}

// A declared version the service already holds is refused rather than stepped
// past.
//
// The conflict walk exists because the listing lags behind a publish, which
// makes it right for a version the CLI derived. Applying it to one an author
// named would publish a version they did not ask for, and report success.
func TestUploadVersionDoesNotWalkPastAConflict(t *testing.T) {
	server := &uploadServer{taken: map[string]bool{"7.0": true}, listing: []string{"7.0"}}
	httpServer := func() *httptest.Server {
		var s *httptest.Server
		s = httptest.NewServer(server.handler(t, func() string { return s.URL }))
		return s
	}()
	t.Cleanup(httpServer.Close)

	client := NewDatasetClientFromPipeline(
		httpServer.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "rows.jsonl"), []byte("{\"query\":\"q\"}\n"), 0o600))

	_, err := client.UploadVersion(context.Background(), "ds", "7.0", dir, "2025-11-15-preview")
	require.Error(t, err, "the version the author named is taken, and that is theirs to resolve")
	assert.True(t, IsVersionConflict(err), "the refusal has to read as a conflict")
	assert.Equal(t, []string{"7.0"}, server.attempts,
		"nothing beyond the declared version is attempted")
}

// When the listing has caught up and is further ahead than the refused
// version, it is the better answer: it skips versions somebody else published.
func TestUploadNextVersionPrefersACaughtUpListing(t *testing.T) {
	server := &uploadServer{
		taken:   map[string]bool{"1.0": true, "2.0": true, "3.0": true},
		listing: []string{"1.0", "2.0", "3.0"},
	}
	httpServer := func() *httptest.Server {
		var s *httptest.Server
		s = httptest.NewServer(server.handler(t, func() string { return s.URL }))
		return s
	}()
	t.Cleanup(httpServer.Close)

	client := NewDatasetClientFromPipeline(
		httpServer.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "rows.jsonl"), []byte("{\"query\":\"q\"}\n"), 0o600))

	ds, err := client.UploadNextVersion(context.Background(), "ds", "", dir, "2025-11-15-preview")
	require.NoError(t, err)
	assert.Equal(t, "4.0", ds.Version)
}

// A service that refuses everything must end in the conflict rather than
// looping: an unbounded walk would hammer the service on a real failure.
func TestUploadNextVersionGivesUpBounded(t *testing.T) {
	server := &uploadServer{taken: map[string]bool{}}
	for _, v := range []string{"1.0", "2.0", "3.0", "4.0", "5.0", "6.0"} {
		server.taken[v] = true
	}
	httpServer := func() *httptest.Server {
		var s *httptest.Server
		s = httptest.NewServer(server.handler(t, func() string { return s.URL }))
		return s
	}()
	t.Cleanup(httpServer.Close)

	client := NewDatasetClientFromPipeline(
		httpServer.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "rows.jsonl"), []byte("{\"query\":\"q\"}\n"), 0o600))

	_, err := client.UploadNextVersion(context.Background(), "ds", "", dir, "2025-11-15-preview")
	require.Error(t, err)
	assert.True(t, IsVersionConflict(err))
	assert.Len(t, server.attempts, versionConflictAttempts)
}
