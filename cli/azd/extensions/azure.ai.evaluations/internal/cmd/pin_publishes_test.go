// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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

// `version` beside `file` is the version to publish, not one to count from.
//
// Moving an already-deployed dataset from an implicit version to a new explicit
// pin leaves the digest unchanged, so reconciliation took the cached path,
// looked the new version up, and refused the deploy over the very version it
// was being asked to create.
//
// The upload itself needs a blob endpoint this fake does not stand up; what is
// asserted is the pivot, which is where the bug was.
func TestANewPinOnUnchangedContentReachesTheUpload(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "golden.jsonl")
	require.NoError(t, os.WriteFile(localPath, []byte("{\"query\":\"hi\"}\n"), 0o600))

	digest, err := project.Fingerprint(localPath)
	require.NoError(t, err)

	var startedUpload bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "startPendingUpload") {
			startedUpload = true
		}
		// Version 5 is the one being asked for and is not there yet.
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/versions/5") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// assert, not require: this runs on the server's goroutine.
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"name": "golden", "version": "5",
		}))
	}))
	t.Cleanup(srv.Close)

	// The last deploy published version 2 from this same file; the author has
	// now pinned 5 without touching the file.
	env := &testEnvServer{state: map[string]string{
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

	_, _, err = r.EnsureDataset(
		t.Context(),
		project.DatasetDecl{Name: "golden", Version: "5", File: "golden.jsonl"},
		localPath,
	)

	assert.True(t, startedUpload,
		"a pin naming a version that is not there is a request to publish it")
	if err != nil {
		assert.NotContains(t, err.Error(), "versions list",
			"and it must not be the refusal that used to end this deploy")
	}
}
