// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A run reads the version reconciliation settled on. Returning a pin without
// recording it left the deploy reporting one version while the run downloaded
// the one recorded before the pin was written -- scoring different rows than
// the author asked for, and labelling the run with the version it did not use.
func TestAPinnedVersionIsRecordedForTheRunToRead(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "golden.jsonl")
	require.NoError(t, os.WriteFile(localPath, []byte("{\"query\":\"hi\"}\n"), 0o600))

	digest, err := project.Fingerprint(localPath)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/versions/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// assert, not require: this runs on the server's goroutine.
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"name": "golden", "version": "1",
		}))
	}))
	t.Cleanup(srv.Close)

	// The last deploy published version 2 from this same file. The author then
	// pinned version 1 without touching the file, so the digest still matches.
	env := &testEnvServer{values: map[string]string{
		project.FingerprintKey("dataset", "golden"): digest,
		versionKey("dataset", "golden"):             "2",
	}}

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	r := &evalReconciler{ec: &evalContext{
		azdClient:     newTestAzdClient(t, env),
		envName:       "test",
		datasetClient: dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline),
	}}

	version, changed, err := r.EnsureDataset(
		context.Background(),
		project.DatasetDecl{Name: "golden", Version: "1"},
		localPath,
	)

	require.NoError(t, err)
	assert.Equal(t, "1", version)
	assert.False(t, changed)
	assert.Equal(t, "1", env.values[versionKey("dataset", "golden")],
		"the run reads this key, so it has to hold what the deploy reported")
}
