// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

type fakeCredential struct{}

func (f *fakeCredential) GetToken(
	_ context.Context,
	_ policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token"}, nil
}

func newTestClient(t *testing.T, handler http.Handler) (*DatasetClient, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	pipeline := runtime.NewPipeline(
		"test",
		"v0.0.0",
		runtime.PipelineOptions{},
		&policy.ClientOptions{},
	)
	client := NewDatasetClientFromPipeline(server.URL, pipeline)
	return client, server
}

// ---------------------------------------------------------------------------
// NewDatasetClient
// ---------------------------------------------------------------------------

func TestNewDatasetClient(t *testing.T) {
	t.Parallel()

	client := NewDatasetClient("https://example.ai.azure.com", &fakeCredential{})
	require.NotNil(t, client)
	assert.Equal(t, "https://example.ai.azure.com", client.endpoint)
}

// ---------------------------------------------------------------------------
// CreateDataset
// ---------------------------------------------------------------------------

func TestCreateDataset_Success(t *testing.T) {
	t.Parallel()

	var capturedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		data, _ := json.Marshal(map[string]any{"name": "my-ds", "version": "v1"})
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.CreateDataset(t.Context(), &CreateDatasetRequest{
		Name:    "my-ds",
		Version: "v1",
		Format:  "jsonl",
		Content: `{"input":"hello"}`,
	}, "2025-11-15-preview")

	require.NoError(t, err)
	assert.Equal(t, "/datasets", capturedPath)
	assert.Equal(t, "my-ds", result.Name)
	assert.Equal(t, "v1", result.Version)
}

// ---------------------------------------------------------------------------
// GetDataset
// ---------------------------------------------------------------------------

func TestGetDataset_Success(t *testing.T) {
	t.Parallel()

	var capturedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		data, _ := json.Marshal(map[string]any{
			"name":     "golden",
			"version":  "v2",
			"blob_uri": "https://storage.blob.core.windows.net/datasets/golden.jsonl",
		})
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.GetDataset(t.Context(), "golden", "v2", "2025-11-15-preview")

	require.NoError(t, err)
	assert.Equal(t, "/datasets/golden/versions/v2", capturedPath)
	assert.Equal(t, "golden", result.Name)
	assert.Equal(t, "v2", result.Version)
	assert.Equal(t, "https://storage.blob.core.windows.net/datasets/golden.jsonl", result.BlobURI)
}

func TestDataset_UnmarshalServicePayload(t *testing.T) {
	t.Parallel()

	// Recorded service GET /datasets/<name>/versions/<ver> response (snake_case).
	payload := `{
		"name": "eval-golden",
		"version": "3",
		"format": "jsonl",
		"blob_uri": "https://store.blob.core.windows.net/ds/eval-golden.jsonl",
		"data_uri": "https://store.blob.core.windows.net/ds/eval-golden-data.jsonl",
		"content_uri": "https://store.blob.core.windows.net/ds/eval-golden-content.jsonl"
	}`

	var ds Dataset
	require.NoError(t, json.Unmarshal([]byte(payload), &ds))

	assert.Equal(t, "eval-golden", ds.Name)
	assert.Equal(t, "3", ds.Version)
	assert.Equal(t, "jsonl", ds.Format)
	assert.Equal(t, "https://store.blob.core.windows.net/ds/eval-golden.jsonl", ds.BlobURI)
	assert.Equal(t, "https://store.blob.core.windows.net/ds/eval-golden-data.jsonl", ds.DataURI)
	assert.Equal(t, "https://store.blob.core.windows.net/ds/eval-golden-content.jsonl", ds.ContentURI)

	// ResolvedBlobURI prefers blob_uri.
	assert.Equal(t, ds.BlobURI, ds.ResolvedBlobURI())

	// When blob_uri is empty, falls back to data_uri.
	ds.BlobURI = ""
	assert.Equal(t, ds.DataURI, ds.ResolvedBlobURI())

	// When both blob_uri and data_uri are empty, falls back to content_uri.
	ds.DataURI = ""
	assert.Equal(t, ds.ContentURI, ds.ResolvedBlobURI())
}

// ---------------------------------------------------------------------------
// GetDatasetCredential
// ---------------------------------------------------------------------------

func TestGetDatasetCredential_Success(t *testing.T) {
	t.Parallel()

	var capturedPath, capturedMethod string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		data, _ := json.Marshal(map[string]any{
			"blob_uri": "https://storage.blob.core.windows.net/datasets/golden.jsonl",
			"sas":      "sig=abc&se=2025-12-31",
		})
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.GetDatasetCredential(t.Context(), "golden", "v2", "2025-11-15-preview")

	require.NoError(t, err)
	assert.Equal(t, "/datasets/golden/versions/v2/credentials", capturedPath)
	assert.Equal(t, http.MethodPost, capturedMethod)
	assert.Equal(t, "https://storage.blob.core.windows.net/datasets/golden.jsonl", result.BlobURI)
	assert.Equal(t, "sig=abc&se=2025-12-31", result.SAS)
}

// ---------------------------------------------------------------------------
// DownloadDataset
// ---------------------------------------------------------------------------

func TestDownloadDataset_Success(t *testing.T) {
	t.Parallel()

	blobContent := `{"input":"hello","expected":"world"}` + "\n" +
		`{"input":"foo","expected":"bar"}` + "\n"

	blobServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(blobContent))
	}))
	t.Cleanup(blobServer.Close)

	client := NewDatasetClient("https://example.ai.azure.com", &fakeCredential{})
	data, err := client.DownloadDataset(t.Context(), blobServer.URL+"/datasets/golden.jsonl?sig=abc")

	require.NoError(t, err)
	assert.Equal(t, blobContent, string(data))
}

func TestDownloadDataset_Error(t *testing.T) {
	t.Parallel()

	blobServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(blobServer.Close)

	client := NewDatasetClient("https://example.ai.azure.com", &fakeCredential{})
	_, err := client.DownloadDataset(t.Context(), blobServer.URL+"/datasets/golden.jsonl?sig=expired")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

func TestGetDataset_NotFound(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	})

	client, _ := newTestClient(t, handler)
	_, err := client.GetDataset(t.Context(), "missing", "v1", "2025-11-15-preview")

	require.Error(t, err)
}
