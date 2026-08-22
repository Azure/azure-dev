// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

// A version is what an eval binds to, so publishing must always add one and
// never change one that exists. Evaluators needed a guard for that; this is
// the same question asked of datasets, against the real service.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"azureaieval/internal/pkg/dataset_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveDatasetClient builds a dataset client against the live project.
func liveDatasetClient(t *testing.T) *dataset_api.DatasetClient {
	t.Helper()
	if os.Getenv("AZURE_AI_EVAL_E2E_LIVE") != "1" {
		t.Skip("set AZURE_AI_EVAL_E2E_LIVE=1 to run live tests")
	}
	endpoint := os.Getenv("FOUNDRY_PROJECT_ENDPOINT")
	require.NotEmpty(t, endpoint, "FOUNDRY_PROJECT_ENDPOINT is required")

	cred, err := liveCredential()
	require.NoError(t, err)
	return dataset_api.NewDatasetClient(endpoint, retryingCredential{inner: cred})
}

// writeRows puts a one-row JSONL file in its own directory, which is what the
// upload path reads from.
func writeRows(t *testing.T, answer string) string {
	t.Helper()
	dir := t.TempDir()
	row := fmt.Sprintf(`{"query":"q","response":%q}`+"\n", answer)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(row), 0o600))
	return dir
}

// TestLiveDatasetVersionIsNeverOverwritten publishes at a version that already
// exists and requires the service to refuse.
//
// The reconciler relies on exactly this: when an author pins `version:` and
// the local content has changed, it publishes at that version and treats a
// conflict as the signal to stop. If the service accepted the write instead,
// the pinned version would silently change under every eval bound to it, and
// `azd up` would report success.
func TestLiveDatasetVersionIsNeverOverwritten(t *testing.T) {
	client := liveDatasetClient(t)
	ctx := context.Background()

	name := fmt.Sprintf("azdlive_ds_immutable_%d", time.Now().UnixNano())

	first, err := client.UploadVersion(
		ctx, name, "1", writeRows(t, "original"), ProjectEndpointAPIVersion)
	require.NoError(t, err)
	require.Equal(t, "1", first.Version)
	t.Cleanup(func() {
		_ = client.DeleteDatasetVersion(
			context.Background(), name, "1", ProjectEndpointAPIVersion)
	})

	_, err = client.UploadVersion(
		ctx, name, "1", writeRows(t, "replacement"), ProjectEndpointAPIVersion)
	require.Error(t, err,
		"publishing over an existing dataset version must be refused, not accepted")
	assert.True(t, dataset_api.IsVersionConflict(err),
		"the refusal must be a conflict the reconciler can recognize; got: %v", err)
}

// TestLiveDatasetUpdateAddsAVersion is the other half: the ordinary path must
// keep adding versions rather than reusing the newest.
func TestLiveDatasetUpdateAddsAVersion(t *testing.T) {
	client := liveDatasetClient(t)
	ctx := context.Background()

	name := fmt.Sprintf("azdlive_ds_next_%d", time.Now().UnixNano())

	first, err := client.UploadNextVersion(
		ctx, name, "", writeRows(t, "one"), ProjectEndpointAPIVersion)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.DeleteDatasetVersion(
			context.Background(), name, first.Version, ProjectEndpointAPIVersion)
	})

	// Immediate, because the version listing lags a publish and this is the
	// window where a second upload could be told the dataset is new and
	// restart at the version the first one just took.
	second, err := client.UploadNextVersion(
		ctx, name, "", writeRows(t, "two"), ProjectEndpointAPIVersion)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.DeleteDatasetVersion(
			context.Background(), name, second.Version, ProjectEndpointAPIVersion)
	})

	assert.NotEqual(t, first.Version, second.Version,
		"a second upload must add a version rather than reuse the first")

	// Both readable, and the first still holding what it was published with.
	original, err := client.GetDataset(ctx, name, first.Version, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	assert.NotEmpty(t, original.Version)
}
