// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAPIVersion = "2025-11-15-preview"

// blobListing is the shape Azure Blob Storage answers a container list with.
func blobListing(names ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><EnumerationResults><Blobs>`)
	for _, n := range names {
		b.WriteString("<Blob><Name>" + n + "</Name></Blob>")
	}
	b.WriteString(`</Blobs></EnumerationResults>`)
	return b.String()
}

// storageServer stands in for both the dataset API and blob storage, recording
// what each leg of a download was asked for.
type storageServer struct {
	mu sync.Mutex

	// credential is the sasUri handed back for a download, relative to the
	// server's own address.
	uriPath string
	// blobs maps a container-relative blob name to its content.
	blobs map[string]string
	// directBlobStatus is the status a direct GET of uriPath answers.
	directBlobStatus int

	gotListQuery url.Values
	gotBlobPaths []string
	gotAPIVer    []string
}

func (s *storageServer) start(t *testing.T) (*DatasetClient, *httptest.Server) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		if v := r.URL.Query().Get("api-version"); v != "" {
			s.gotAPIVer = append(s.gotAPIVer, v)
		}

		switch {
		case strings.HasSuffix(r.URL.Path, "/credentials"):
			w.Header().Set("Content-Type", "application/json")
			// assert, not require: this runs on the server's goroutine, and
			// FailNow there aborts mid-response and fails whichever test is
			// running instead.
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"blobReferenceForConsumption": map[string]any{
					"credential": map[string]any{"sasUri": srv.URL + s.uriPath + "?sig=secret"},
				},
			}))

		case r.URL.Query().Get("comp") == "list":
			s.gotListQuery = r.URL.Query()
			names := make([]string, 0, len(s.blobs))
			for n := range s.blobs {
				names = append(names, n)
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(blobListing(names...)))

		// A direct read of the credential URI itself, keyed under "".
		case r.URL.Path == s.uriPath:
			s.gotBlobPaths = append(s.gotBlobPaths, r.URL.Path)
			if s.directBlobStatus != 0 {
				w.WriteHeader(s.directBlobStatus)
				return
			}
			_, _ = w.Write([]byte(s.blobs[""]))

		default:
			s.gotBlobPaths = append(s.gotBlobPaths, r.URL.Path)
			body, ok := s.blobs[strings.TrimPrefix(r.URL.Path, s.uriPath+"/")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)

	client := NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))
	return client, srv
}

// A dataset that was uploaded names its own file, so it reads in one hop and
// the container must never be listed.
func TestDownloadDatasetContentReadsABlobURIDirectly(t *testing.T) {
	server := &storageServer{
		uriPath: "/c/rows.jsonl",
		blobs:   map[string]string{"": `{"query":"direct"}`},
	}
	client, _ := server.start(t)

	data, err := client.DownloadDatasetContent(context.Background(), "ds", "1.0", testAPIVersion)
	require.NoError(t, err)
	assert.Equal(t, `{"query":"direct"}`, string(data))
	assert.Nil(t, server.gotListQuery, "a blob URI needs no container listing")
}

// A generated dataset names the container it was written into, and nothing in
// the payload says so: isSingleFile is true either way. Reading the container
// directly returns a 409, so the blob inside has to be found first.
func TestDownloadDatasetContentListsAContainerURI(t *testing.T) {
	server := &storageServer{
		uriPath: "/generated-container",
		blobs: map[string]string{
			"_meta.json": `{"ignored":true}`,
			"data.jsonl": `{"query":"from the container"}`,
		},
	}
	client, _ := server.start(t)

	data, err := client.DownloadDatasetContent(context.Background(), "ds", "1.0", testAPIVersion)
	require.NoError(t, err)
	assert.Equal(t, `{"query":"from the container"}`, string(data),
		"the JSONL is chosen over the metadata sitting beside it")
	require.NotNil(t, server.gotListQuery)
	assert.Equal(t, "container", server.gotListQuery.Get("restype"))
	assert.Equal(t, "secret", server.gotListQuery.Get("sig"),
		"the listing must keep the SAS token, or storage answers 403")
}

// A URI can name a file and still be a container — the extension is a guess,
// not a fact. When the direct read fails the listing is the fallback, so the
// download succeeds rather than surfacing the first status.
func TestDownloadDatasetContentFallsBackWhenTheBlobReadFails(t *testing.T) {
	server := &storageServer{
		uriPath:          "/c/looks.jsonl",
		directBlobStatus: http.StatusConflict,
		blobs:            map[string]string{"real.jsonl": `{"query":"found by listing"}`},
	}
	client, _ := server.start(t)

	data, err := client.DownloadDatasetContent(context.Background(), "ds", "1.0", testAPIVersion)
	require.NoError(t, err, "a 409 on the direct read is the container case, not a failure")
	assert.Equal(t, `{"query":"found by listing"}`, string(data))
	assert.NotNil(t, server.gotListQuery)
}

// An empty container is a dataset with nothing to read, and saying so beats
// returning empty content that looks like a dataset with no rows.
func TestDownloadDatasetContentReportsAnEmptyContainer(t *testing.T) {
	server := &storageServer{uriPath: "/empty", blobs: map[string]string{}}
	client, _ := server.start(t)

	_, err := client.DownloadDatasetContent(context.Background(), "ds", "1.0", testAPIVersion)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no downloadable file")
}

// The URI carries no SAS of its own, so a credential that resolves to nothing
// has to be reported here rather than as an unauthorized read later.
func TestDownloadDatasetContentRequiresADownloadURI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	client := NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	_, err := client.DownloadDatasetContent(context.Background(), "ds", "1.0", testAPIVersion)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no download URI")
}

// The blob name is appended to the container path, and the SAS token stays on
// the query where storage expects it.
func TestDownloadBlobKeepsTheSASToken(t *testing.T) {
	var gotPath, gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotSig = r.URL.Path, r.URL.Query().Get("sig")
		_, _ = w.Write([]byte("rows"))
	}))
	t.Cleanup(srv.Close)

	client := NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	data, err := client.DownloadBlob(context.Background(), srv.URL+"/container?sig=secret", "data.jsonl")
	require.NoError(t, err)
	assert.Equal(t, "rows", string(data))
	assert.Equal(t, "/container/data.jsonl", gotPath)
	assert.Equal(t, "secret", gotSig)
}

// A storage failure has to name the blob, since the container holds several
// and the status alone does not say which one was refused.
func TestDownloadBlobReportsTheStatusAndName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	client := NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	_, err := client.DownloadBlob(context.Background(), srv.URL+"/c", "data.jsonl")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "data.jsonl")
}

// A malformed URI is the caller's mistake, and it is worth catching before a
// request goes out against a half-parsed address.
func TestBlobOperationsRejectAnUnparseableURI(t *testing.T) {
	client := NewDatasetClientFromPipeline(
		"https://example", runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	_, err := client.DownloadBlob(context.Background(), "://nope", "x.jsonl")
	require.Error(t, err)

	_, err = client.ListContainerBlobs(context.Background(), "://nope")
	require.Error(t, err)

	err = client.UploadBlob(context.Background(), "://nope", "x.jsonl", []byte("{}"))
	require.Error(t, err)
}

// Storage answers a listing in XML, and a shape that does not parse yields no
// names rather than a panic.
func TestParseBlobNames(t *testing.T) {
	assert.Equal(t, []string{"a.jsonl", "b.json"},
		parseBlobNames(blobListing("a.jsonl", "b.json")))
	assert.Empty(t, parseBlobNames(blobListing()))
	assert.Empty(t, parseBlobNames("not xml at all"),
		"an unreadable listing is an empty one, not a crash")
	assert.Empty(t, parseBlobNames(
		`<EnumerationResults><Blobs><Blob><Name></Name></Blob></Blobs></EnumerationResults>`),
		"a nameless blob cannot be downloaded, so it is not offered")
}
