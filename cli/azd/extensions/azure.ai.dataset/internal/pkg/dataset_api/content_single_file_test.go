// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDatasetContentRequiresConfirmedSingleFile(t *testing.T) {
	for _, tc := range []struct {
		name           string
		pages          []string
		metadata       string
		metadataStatus int
		wantFiles      []string
		wantSingle     bool
		wantMetadata   bool
		wantError      string
	}{
		{
			name:  "container scoped single file",
			pages: []string{blobPage("", "data.jsonl")}, metadata: `{"isSingleFile":true}`,
			wantFiles: []string{"data.jsonl"}, wantSingle: true, wantMetadata: true,
		},
		{
			name:  "one file folder",
			pages: []string{blobPage("", "nested/data.jsonl")}, metadata: `{"isSingleFile":false}`,
			wantFiles: []string{"nested/data.jsonl"}, wantMetadata: true,
		},
		{
			name:  "missing shape stays a folder",
			pages: []string{blobPage("", "data.jsonl")}, metadata: `{}`,
			wantFiles: []string{"data.jsonl"}, wantMetadata: true,
		},
		{
			name:  "directory markers are not files",
			pages: []string{blobPage("", "", "nested/", "nested/data.jsonl")}, metadata: `{"isSingleFile":true}`,
			wantFiles: []string{"nested/data.jsonl"}, wantSingle: true, wantMetadata: true,
		},
		{
			name:  "one file after an empty first page",
			pages: []string{blobPage("next"), blobPage("", "data.jsonl")}, metadata: `{"isSingleFile":true}`,
			wantFiles: []string{"data.jsonl"}, wantSingle: true, wantMetadata: true,
		},
		{
			name:     "later page contains another file",
			pages:    []string{blobPage("next", "data.jsonl"), blobPage("", "_meta.json")},
			metadata: `{"isSingleFile":true}`, wantFiles: []string{"_meta.json", "data.jsonl"},
		},
		{
			name:  "empty container",
			pages: []string{blobPage("", "nested/")}, wantError: "no downloadable file",
		},
		{
			name:      "incomplete listing",
			pages:     []string{blobPage("next", "data.jsonl"), blobPage("next")},
			wantError: "incomplete",
		},
		{
			name:      "unreadable later page",
			pages:     []string{blobPage("next", "data.jsonl"), `<EnumerationResults>`},
			wantError: "parse list response",
		},
		{
			name:  "metadata error is not a folder",
			pages: []string{blobPage("", "data.jsonl")}, metadataStatus: http.StatusForbidden,
			metadata: `{"error":{"code":"Forbidden"}}`, wantMetadata: true, wantError: "reading dataset",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var base string
			var listed, metadataReads int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/credentials"):
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"blobReferenceForConsumption":{"credential":{"sasUri":%q}}}`,
						base+"/container?sig=test-signature")
				case r.URL.Path == "/datasets/sample/versions/2.0":
					metadataReads++
					assert.Equal(t, http.MethodGet, r.Method)
					assert.Equal(t, testAPIVersion, r.URL.Query().Get("api-version"))
					w.Header().Set("Content-Type", "application/json")
					if tc.metadataStatus != 0 {
						w.WriteHeader(tc.metadataStatus)
					}
					fmt.Fprint(w, tc.metadata)
				case r.URL.Query().Get("comp") == "list":
					assert.Less(t, listed, len(tc.pages))
					if listed >= len(tc.pages) {
						http.Error(w, "unexpected extra listing", http.StatusBadRequest)
						return
					}
					fmt.Fprint(w, tc.pages[listed])
					listed++
				default:
					w.WriteHeader(http.StatusForbidden)
				}
			}))
			t.Cleanup(server.Close)
			base = server.URL
			content, err := clientFor(server).ListDatasetContent(t.Context(), "sample", "2.0", testAPIVersion)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.Nil(t, content)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantFiles, content.Files)
				assert.Equal(t, tc.wantSingle, content.SingleFile)
			}
			assert.Equal(t, tc.wantMetadata, metadataReads == 1)
			assert.Equal(t, len(tc.pages), listed)
		})
	}
}

func TestDatasetContentErrorsRedactCredentialURLs(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	for _, raw := range []string{
		"https://private-user:private-password@example.invalid/container\x7f?sig=private-signature#private-fragment",
		"unsupported://private-user:private-password@example.invalid/container?sig=private-signature#private-fragment",
	} {
		t.Run(strings.Split(raw, ":")[0], func(t *testing.T) {
			logs.Reset()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"sas_uri": raw}))
			}))
			t.Cleanup(server.Close)
			client := clientFor(server)
			_, err := client.ListDatasetContent(t.Context(), "sample", "1.0", testAPIVersion)
			require.Error(t, err)
			listError := err.Error()
			_, err = client.Open(t.Context(), &DatasetContent{Container: raw}, "data.jsonl")
			require.Error(t, err)
			for _, secret := range []string{
				"private-user", "private-password", "private-signature", "private-fragment", "sig=",
			} {
				assert.NotContains(t, listError, secret)
				assert.NotContains(t, err.Error(), secret)
				assert.NotContains(t, logs.String(), secret)
			}
		})
	}
}

func TestDatasetContentOpenKeepsContainerSASAndRelativeName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/container/nested/one file.jsonl", r.URL.Path)
		assert.Equal(t, "test-signature", r.URL.Query().Get("sig"))
		assert.Empty(t, r.Header.Get("Authorization"))
		fmt.Fprint(w, "rows\n")
	}))
	t.Cleanup(server.Close)
	content := &DatasetContent{
		Container: server.URL + "/container?sig=test-signature",
		Files:     []string{"nested/one file.jsonl"}, SingleFile: true,
	}
	body, err := clientFor(server).Open(t.Context(), content, content.Files[0])
	require.NoError(t, err)
	defer body.Close()
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "rows\n", string(got))
}
