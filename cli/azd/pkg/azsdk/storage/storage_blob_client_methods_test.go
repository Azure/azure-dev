// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/stretchr/testify/require"
)

// newBlobTestServer returns an httptest server that simulates the subset of
// the Azure Blob REST API used by blobClient operations.
// Behavior:
//   - GET ?comp=list (list containers) returns the container named in
//     `containerName` (so ensureContainerExists sees it and doesn't create it).
//   - GET /{container}?restype=container&comp=list returns zero blobs.
//   - GET /{container}/{blob} returns `downloadBody` bytes.
//   - PUT /{container}/{blob} (upload) returns 201.
//   - DELETE /{container}/{blob} returns 202.
//   - Every other request returns 200 (a harmless default).
func newBlobTestServer(t *testing.T, containerName, downloadBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC().Format(http.TimeFormat)
		w.Header().Set("x-ms-request-id", "test-req-id")
		w.Header().Set("x-ms-version", "2023-11-03")
		w.Header().Set("Date", now)
		w.Header().Set("Last-Modified", now)
		w.Header().Set("ETag", `"0x8D0000000000000"`)

		q := r.URL.Query()
		comp := q.Get("comp")
		restype := q.Get("restype")
		path := strings.Trim(r.URL.Path, "/")

		// List containers at service root: GET /?comp=list
		if r.Method == http.MethodGet && path == "" && comp == "list" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>` +
				`<EnumerationResults ServiceEndpoint="https://example/">` +
				`<Containers><Container><Name>` + containerName + `</Name>` +
				`<Properties><Last-Modified>` + now + `</Last-Modified>` +
				`<Etag>0x8D</Etag></Properties></Container></Containers>` +
				`<NextMarker/></EnumerationResults>`))
			return
		}

		// List blobs inside a container: GET /{container}?restype=container&comp=list
		if r.Method == http.MethodGet && restype == "container" && comp == "list" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>` +
				`<EnumerationResults ServiceEndpoint="https://example/" ContainerName="` +
				containerName + `"><Blobs></Blobs><NextMarker/></EnumerationResults>`))
			return
		}

		// Download blob: GET /{container}/{blob}
		if r.Method == http.MethodGet && strings.Contains(path, "/") {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("x-ms-blob-type", "BlockBlob")
			w.Header().Set("x-ms-creation-time", now)
			w.Header().Set("Content-Length", "")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(downloadBody))
			return
		}

		// Upload blob: PUT /{container}/{blob}
		if r.Method == http.MethodPut && strings.Contains(path, "/") {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusCreated)
			return
		}

		// Delete blob: DELETE /{container}/{blob}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newBlobClientForServer(t *testing.T, srv *httptest.Server, containerName string) BlobClient {
	t.Helper()
	client, err := azblob.NewClient(srv.URL, fakeTokenCredential{}, &azblob.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: srv.Client(),
			Retry: policy.RetryOptions{
				MaxRetries: -1,
			},
		},
	})
	require.NoError(t, err)
	return NewBlobClient(&AccountConfig{
		AccountName:   "testaccount",
		ContainerName: containerName,
	}, client)
}

func Test_BlobClient_Items_ReturnsEmptyList(t *testing.T) {
	t.Parallel()

	srv := newBlobTestServer(t, "testcontainer", "")
	bc := newBlobClientForServer(t, srv, "testcontainer")

	blobs, err := bc.Items(t.Context())
	require.NoError(t, err)
	require.NotNil(t, blobs)
	require.Empty(t, blobs)
}

func Test_BlobClient_Download_ReturnsBody(t *testing.T) {
	t.Parallel()

	srv := newBlobTestServer(t, "testcontainer", "hello blob")
	bc := newBlobClientForServer(t, srv, "testcontainer")

	rc, err := bc.Download(t.Context(), "some/blob.txt")
	require.NoError(t, err)
	defer rc.Close()

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "hello blob", string(data))
}

func Test_BlobClient_Upload_Succeeds(t *testing.T) {
	t.Parallel()

	srv := newBlobTestServer(t, "testcontainer", "")
	bc := newBlobClientForServer(t, srv, "testcontainer")

	err := bc.Upload(t.Context(), "foo.txt", strings.NewReader("payload"))
	require.NoError(t, err)
}

func Test_BlobClient_Delete_Succeeds(t *testing.T) {
	t.Parallel()

	srv := newBlobTestServer(t, "testcontainer", "")
	bc := newBlobClientForServer(t, srv, "testcontainer")

	err := bc.Delete(t.Context(), "foo.txt")
	require.NoError(t, err)
}

// Test_BlobClient_EnsureContainer_CreatesWhenMissing verifies that if the
// listed containers don't include the configured one, ensureContainerExists
// issues a CreateContainer call.
func Test_BlobClient_EnsureContainer_CreatesWhenMissing(t *testing.T) {
	t.Parallel()

	createCalled := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC().Format(http.TimeFormat)
		w.Header().Set("x-ms-request-id", "test-req-id")
		w.Header().Set("x-ms-version", "2023-11-03")
		w.Header().Set("Date", now)
		w.Header().Set("Last-Modified", now)
		w.Header().Set("ETag", `"0x8D"`)

		q := r.URL.Query()
		comp := q.Get("comp")
		restype := q.Get("restype")
		path := strings.Trim(r.URL.Path, "/")

		// List containers: return an empty list so the client creates one.
		if r.Method == http.MethodGet && path == "" && comp == "list" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>` +
				`<EnumerationResults ServiceEndpoint="https://example/">` +
				`<Containers></Containers><NextMarker/></EnumerationResults>`))
			return
		}
		// Create container: PUT /{container}?restype=container
		if r.Method == http.MethodPut && restype == "container" {
			createCalled = true
			w.WriteHeader(http.StatusCreated)
			return
		}
		// List blobs inside container.
		if r.Method == http.MethodGet && restype == "container" && comp == "list" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>` +
				`<EnumerationResults ServiceEndpoint="https://example/" ContainerName="brand-new">` +
				`<Blobs></Blobs><NextMarker/></EnumerationResults>`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	bc := newBlobClientForServer(t, srv, "brand-new")

	blobs, err := bc.Items(t.Context())
	require.NoError(t, err)
	require.Empty(t, blobs)
	require.True(t, createCalled, "CreateContainer should be called when not found")
}
