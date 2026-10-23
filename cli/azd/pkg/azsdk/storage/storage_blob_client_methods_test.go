// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"errors"
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

const testBlobETag = `"0x8D0000000000000"`

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
	return newBlobTestServerWithPutHook(t, containerName, downloadBody, nil, 0)
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

	download, err := bc.Download(t.Context(), "some/blob.txt")
	require.NoError(t, err)
	defer download.Body.Close()

	data, err := io.ReadAll(download.Body)
	require.NoError(t, err)
	require.Equal(t, "hello blob", string(data))
	require.Equal(t, azcore.ETag(testBlobETag), download.ETag)
}

func Test_BlobClient_Upload_Succeeds(t *testing.T) {
	t.Parallel()

	srv := newBlobTestServer(t, "testcontainer", "")
	bc := newBlobClientForServer(t, srv, "testcontainer")

	_, err := bc.Upload(t.Context(), "foo.txt", strings.NewReader("payload"), "")
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

func Test_BlobClient_Upload_SendsIfMatchFromDownload(t *testing.T) {
	t.Parallel()

	var ifMatch, ifNoneMatch string
	srv := newBlobTestServerWithPutHook(t, "testcontainer", "body", func(r *http.Request) {
		ifMatch = r.Header.Get("If-Match")
		ifNoneMatch = r.Header.Get("If-None-Match")
	}, 0)
	bc := newBlobClientForServer(t, srv, "testcontainer")

	download, err := bc.Download(t.Context(), "foo.txt")
	require.NoError(t, err)
	require.NoError(t, download.Body.Close())

	_, err = bc.Upload(t.Context(), "foo.txt", strings.NewReader("next"), download.ETag)
	require.NoError(t, err)
	require.Equal(t, string(download.ETag), ifMatch)
	require.Empty(t, ifNoneMatch)
}

func Test_BlobClient_Upload_NewBlobUsesIfNoneMatchStar(t *testing.T) {
	t.Parallel()

	var ifMatch, ifNoneMatch string
	srv := newBlobTestServerWithPutHook(t, "testcontainer", "", func(r *http.Request) {
		ifMatch = r.Header.Get("If-Match")
		ifNoneMatch = r.Header.Get("If-None-Match")
	}, 0)
	bc := newBlobClientForServer(t, srv, "testcontainer")

	_, err := bc.Upload(t.Context(), "new.txt", strings.NewReader("payload"), "")
	require.NoError(t, err)
	require.Empty(t, ifMatch)
	require.Equal(t, "*", ifNoneMatch)
}

func Test_BlobClient_Upload_ConditionNotMet(t *testing.T) {
	t.Parallel()

	srv := newBlobTestServerWithPutHook(
		t, "testcontainer", "", nil, http.StatusPreconditionFailed)
	bc := newBlobClientForServer(t, srv, "testcontainer")
	_, err := bc.Upload(t.Context(), "foo.txt", strings.NewReader("payload"), `"stale-etag"`)
	require.ErrorIs(t, err, ErrConcurrencyConflict)

	conflict, ok := errors.AsType[*ConcurrencyConflictError](err)
	require.True(t, ok)
	require.Equal(t, "foo.txt", conflict.BlobPath)
}

func newBlobTestServerWithPutHook(
	t *testing.T, containerName, downloadBody string, onPut func(*http.Request), putStatus int,
) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC().Format(http.TimeFormat)
		w.Header().Set("x-ms-request-id", "test-req-id")
		w.Header().Set("x-ms-version", "2023-11-03")
		w.Header().Set("Date", now)
		w.Header().Set("Last-Modified", now)
		w.Header().Set("ETag", testBlobETag)

		q := r.URL.Query()
		comp := q.Get("comp")
		restype := q.Get("restype")
		path := strings.Trim(r.URL.Path, "/")

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

		if r.Method == http.MethodGet && restype == "container" && comp == "list" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>` +
				`<EnumerationResults ServiceEndpoint="https://example/" ContainerName="` +
				containerName + `"><Blobs></Blobs><NextMarker/></EnumerationResults>`))
			return
		}

		if r.Method == http.MethodGet && strings.Contains(path, "/") {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("x-ms-blob-type", "BlockBlob")
			w.Header().Set("x-ms-creation-time", now)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(downloadBody))
			return
		}

		if r.Method == http.MethodPut && strings.Contains(path, "/") {
			if onPut != nil {
				onPut(r)
			}
			_, _ = io.Copy(io.Discard, r.Body)
			if putStatus == http.StatusPreconditionFailed {
				w.Header().Set("x-ms-error-code", "ConditionNotMet")
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = w.Write([]byte(
					`<?xml version="1.0" encoding="utf-8"?><Error>` +
						`<Code>ConditionNotMet</Code>` +
						`<Message>The condition specified using HTTP conditional header(s) is not met.` +
						`</Message></Error>`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			return
		}

		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}
