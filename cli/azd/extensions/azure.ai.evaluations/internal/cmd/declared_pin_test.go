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

// writeConfigWithPin writes a configuration whose dataset carries the given
// `version:`, or none when it is empty.
func writeConfigWithPin(t *testing.T, pin string) string {
	t.Helper()

	version := ""
	if pin != "" {
		version = "\n    version: \"" + pin + "\""
	}
	body := `datasets:
  - name: golden` + version + `
evals:
  - name: quality
    dataset: golden
    evaluators:
      - evaluator: builtin.relevance
`
	dir := t.TempDir()
	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// A pin is the author saying which rows to score, and the run reads it from the
// declaration. Reading it out of the environment instead meant the deploy had
// to overwrite the version the dataset's content published -- which is the
// drift baseline, so removing the pin later failed the next deploy with a
// report that something had been published behind the configuration's back.
func TestARunReadsThePinnedVersionFromTheDeclaration(t *testing.T) {
	group := &project.Eval{Name: "quality", Dataset: "golden"}

	assert.Equal(t, "1", declaredDatasetVersion(writeConfigWithPin(t, "1"), group),
		"the declaration is what says which version to score")
	assert.Empty(t, declaredDatasetVersion(writeConfigWithPin(t, ""), group),
		"no pin leaves the recorded version to answer")
}

// An eval reached by id has no configuration to ask, and a declaration for a
// different dataset says nothing about this one.
func TestNoPinIsReadWhenThereIsNothingToReadItFrom(t *testing.T) {
	group := &project.Eval{Name: "quality", Dataset: "golden"}

	assert.Empty(t, declaredDatasetVersion("", group))
	assert.Empty(t, declaredDatasetVersion(writeConfigWithPin(t, "1"), nil))
	assert.Empty(t, declaredDatasetVersion(
		writeConfigWithPin(t, "1"), &project.Eval{Name: "quality", Dataset: "other"}))
}

// The deploy leaves the recorded version alone, so it keeps meaning "the
// version this file's content published" and the drift check keeps working.
//
// Driven through EnsureDataset rather than asserted against the source, so
// respelling the write does not slip past.
func TestPinningDoesNotOverwriteTheVersionTheContentPublished(t *testing.T) {
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
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"name": "golden", "version": "1"}))
	}))
	t.Cleanup(srv.Close)

	// The last deploy published version 2 from this same file. The author then
	// pinned version 1 without touching it, so the digest still matches.
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

	version, changed, err := r.EnsureDataset(
		context.Background(),
		project.DatasetDecl{Name: "golden", Version: "1"},
		localPath,
	)

	require.NoError(t, err)
	assert.Equal(t, "1", version, "the pin is what the deploy reports")
	assert.False(t, changed)
	assert.Equal(t, "2", env.stored(t, versionKey("dataset", "golden")),
		"the recorded version still means what this file published, "+
			"or removing the pin later reads as somebody publishing behind the config")
}

// And the pin is what the run downloads. Reading the declaration and then not
// using it would leave the run on the recorded version, which is the failure
// this whole path exists to avoid.
func TestTheDownloadedRowsAreThePinnedVersion(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		// assert, not require: this runs on the server's goroutine.
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"blobReferenceForConsumption": map[string]any{
				"credential": map[string]any{"sasUri": "https://example.invalid/blob"},
			},
		}))
	}))
	t.Cleanup(srv.Close)

	env := &testEnvServer{state: map[string]string{
		versionKey("dataset", "golden"): "2",
	}}
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec := &evalContext{
		azdClient:     newTestAzdClient(t, env),
		envName:       "test",
		datasetClient: dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline),
	}

	// The download itself cannot succeed against this fake; what is under test
	// is the version it asked the service for.
	_, _ = ec.readRegisteredDataset(context.Background(), "golden", "1", 0)

	require.NotEmpty(t, asked)
	assert.Contains(t, strings.Join(asked, " "), "/versions/1",
		"the pin the declaration carries is the version to score")
	assert.NotContains(t, strings.Join(asked, " "), "/versions/2",
		"the recorded version is what the pin overrides")
}
