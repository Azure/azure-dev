// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/pkg/dataset_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type downloadRecording struct {
	mu       sync.Mutex
	requests []string
}

func (r *downloadRecording) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

func downloadClient(
	t *testing.T,
	singleFile bool,
	files map[string]string,
) (*dataset_api.DatasetClient, *downloadRecording) {
	t.Helper()
	recording := &downloadRecording{}
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dataURI := base + "/container"
		if singleFile {
			dataURI += "/data.jsonl"
		}
		request := r.Method + " " + r.URL.Path
		if r.URL.Query().Get("comp") == "list" {
			request += "?comp=list"
		}
		recording.mu.Lock()
		recording.requests = append(recording.requests, request)
		recording.mu.Unlock()

		if strings.HasPrefix(r.URL.Path, "/datasets/") {
			assert.Equal(t, ProjectEndpointAPIVersion, r.URL.Query().Get("api-version"))
			w.Header().Set("Content-Type", "application/json")
		} else {
			assert.Equal(t, "test-signature", r.URL.Query().Get("sig"))
			assert.Equal(t, "c", r.URL.Query().Get("sr"))
			assert.Empty(t, r.Header.Get("Authorization"))
		}
		switch {
		case r.URL.Path == "/datasets/sample/versions/1.0/credentials":
			assert.Equal(t, http.MethodPost, r.Method)
			reference := map[string]any{
				"blobUri":             dataURI,
				"storageAccountArmId": "test-storage-account-id",
				"credential": map[string]string{
					"type": "SAS", "credentialType": "SAS", "sasUri": base + "/container?sr=c&sig=test-signature",
				},
				"blobManifestDigest": nil,
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"blobReference": reference, "blobReferenceForConsumption": reference,
			}))
		case r.URL.Path == "/datasets/sample/versions/1.0":
			assert.Equal(t, http.MethodGet, r.Method)
			datasetType := "uri_folder"
			if singleFile {
				datasetType = "uri_file"
			}
			fmt.Fprintf(w, `{"name":"sample","version":"1.0","type":%q,"isSingleFile":%t,"dataUri":%q}`,
				datasetType, singleFile, dataURI)
		case r.URL.Path == "/datasets/sample/versions":
			fmt.Fprint(w, `{"value":[{"name":"sample","version":"1.0"}]}`)
		case r.URL.Path == "/container" && r.URL.Query().Get("comp") == "list":
			assert.Equal(t, "container", r.URL.Query().Get("restype"))
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<EnumerationResults><Blobs>`)
			for _, file := range slices.Sorted(maps.Keys(files)) {
				fmt.Fprintf(w, `<Blob><Name>%s</Name></Blob>`, file)
			}
			fmt.Fprint(w, `</Blobs></EnumerationResults>`)
		default:
			body, ok := files[strings.TrimPrefix(r.URL.Path, "/container/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, body)
		}
	}))
	t.Cleanup(server.Close)
	base = server.URL
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return dataset_api.NewDatasetClientFromPipeline(base, pipeline), recording
}

func downloadAction(t *testing.T, dir, outFile string) (*datasetDownloadAction, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	command := &cobra.Command{Use: "download"}
	command.Flags().StringP("output", "o", outputJSON, "")
	command.Flags().Bool("no-prompt", true, "")
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	return &datasetDownloadAction{
		cmd: command, name: "sample", version: "1.0", outputDir: dir, outFile: outFile,
	}, &stdout, &stderr
}

func TestDownloadContainerBackedSingleFileWritesExactDestination(t *testing.T) {
	const rows = "{\"query\":\"first row\"}\n{\"query\":\"second row\"}\n"
	client, recording := downloadClient(t, true, map[string]string{"data.jsonl": rows})
	dest := filepath.Join(t.TempDir(), "new parent", "chosen.jsonl")
	action, stdout, stderr := downloadAction(t, "", dest)

	require.NoError(t, action.downloadWith(t.Context(), &evalContext{datasetClient: client}))
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, rows, string(got))
	var document map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &document))
	assert.Equal(t, map[string]any{
		"dataset": "sample", "version": "1.0", "path": dest, "files": float64(1), "singleFile": true,
	}, document)
	assert.Empty(t, stderr.String())
	assert.Equal(t, []string{
		"POST /datasets/sample/versions/1.0/credentials",
		"GET /container?comp=list",
		"GET /datasets/sample/versions/1.0",
		"GET /container/data.jsonl",
	}, recording.snapshot())
}

func TestDownloadContainerBackedFileDestinationsAndOverwrite(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outFile  bool
		existing bool
		force    bool
	}{
		{name: "default file name"},
		{name: "exact file name", outFile: true},
		{name: "refuse existing file", outFile: true, existing: true},
		{name: "force existing file", outFile: true, existing: true, force: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := downloadClient(t, true, map[string]string{"data.jsonl": "new rows\n"})
			dir := t.TempDir()
			dest := filepath.Join(dir, "sample-1.0.jsonl")
			action, stdout, _ := downloadAction(t, dir, "")
			if tc.outFile {
				dest = filepath.Join(dir, "chosen.txt")
				action.outputDir, action.outFile = "", dest
			}
			if tc.existing {
				require.NoError(t, os.WriteFile(dest, []byte("original\n"), 0o600))
			}
			action.force = tc.force
			action.version = "" // The resolved version must be used for both metadata and credentials.
			err := action.downloadWith(t.Context(), &evalContext{datasetClient: client})
			if tc.existing && !tc.force {
				require.ErrorContains(t, err, "--force")
				assert.Empty(t, stdout.String())
			} else {
				require.NoError(t, err)
				assert.Contains(t, stdout.String(), `"version": "1.0"`)
			}
			got, err := os.ReadFile(dest)
			require.NoError(t, err)
			want := "new rows\n"
			if tc.existing && !tc.force {
				want = "original\n"
			}
			assert.Equal(t, want, string(got))
		})
	}
}

func TestDownloadContainerBackedFileDefaultsToCurrentDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	client, _ := downloadClient(t, true, map[string]string{"data.jsonl": "rows\n"})
	action, stdout, stderr := downloadAction(t, "", "")
	require.NoError(t, action.downloadWith(t.Context(), &evalContext{datasetClient: client}))
	got, err := os.ReadFile("sample-1.0.jsonl")
	require.NoError(t, err)
	assert.Equal(t, "rows\n", string(got))
	assert.Contains(t, stdout.String(), `"path": "sample-1.0.jsonl"`)
	assert.Empty(t, stderr.String())
}

func TestDownloadFolderPreservesEveryFile(t *testing.T) {
	for _, tc := range []struct {
		name       string
		singleFile bool
		files      map[string]string
	}{
		{name: "one file folder", files: map[string]string{"nested/data.jsonl": "rows\n"}},
		{name: "multiple files despite metadata", singleFile: true, files: map[string]string{
			"_meta.json": "{}\n", "nested/data.jsonl": "rows\n",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, recording := downloadClient(t, tc.singleFile, tc.files)
			dir := t.TempDir()
			action, stdout, _ := downloadAction(t, "", filepath.Join(dir, "must-not-exist.jsonl"))
			ec := &evalContext{datasetClient: client}
			require.ErrorContains(t, action.downloadWith(t.Context(), ec), "--output-dir")
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Empty(t, entries)
			assert.Empty(t, stdout.String())
			for _, request := range recording.snapshot() {
				assert.NotContains(t, request, "GET /container/")
			}

			action.outFile, action.outputDir = "", dir
			require.NoError(t, action.cmd.Flags().Set("output", "table"))
			require.NoError(t, action.downloadWith(t.Context(), ec))
			for name, want := range tc.files {
				got, err := os.ReadFile(filepath.Join(dir, "sample-1.0", filepath.FromSlash(name)))
				require.NoError(t, err)
				assert.Equal(t, want, string(got))
			}
			assert.Contains(t, stdout.String(), "Downloaded dataset")
		})
	}
}
