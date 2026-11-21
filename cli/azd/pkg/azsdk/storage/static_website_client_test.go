// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

type recordedRequest struct {
	method string
	path   string
	query  string
	header http.Header
	body   string
}

func newStaticWebsiteTestClient(
	t *testing.T,
	handler func(w http.ResponseWriter, r *http.Request),
) (*StaticWebsiteClient, *[]recordedRequest) {
	t.Helper()

	var mu sync.Mutex
	requests := []recordedRequest{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, recordedRequest{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			header: r.Header.Clone(),
			body:   string(body),
		})
		mu.Unlock()

		w.Header().Set("x-ms-request-id", "test")
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	client, err := NewStaticWebsiteClient(srv.URL, fakeTokenCredential{}, &azcore.ClientOptions{
		Transport: srv.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	})
	require.NoError(t, err)

	return client, &requests
}

func Test_StaticWebsiteClient_GetStaticWebsite(t *testing.T) {
	t.Parallel()

	client, _ := newStaticWebsiteTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><StorageServiceProperties>` +
			`<StaticWebsite><Enabled>true</Enabled><IndexDocument>home.html</IndexDocument>` +
			`<ErrorDocument404Path>missing.html</ErrorDocument404Path></StaticWebsite>` +
			`</StorageServiceProperties>`))
	})

	props, err := client.GetStaticWebsite(t.Context())
	require.NoError(t, err)
	require.Equal(t, &StaticWebsiteProperties{
		Enabled:              true,
		IndexDocument:        "home.html",
		ErrorDocument404Path: "missing.html",
	}, props)
}

func Test_StaticWebsiteClient_EnableStaticWebsite_OnlySendsStaticWebsite(t *testing.T) {
	t.Parallel()

	client, requests := newStaticWebsiteTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	require.NoError(t, client.EnableStaticWebsite(t.Context(), "index.html", "404.html"))
	require.Len(t, *requests, 1)

	request := (*requests)[0]
	require.Equal(t, http.MethodPut, request.method)
	require.Contains(t, request.query, "comp=properties")
	require.Contains(t, request.body, "<Enabled>true</Enabled>")
	require.Contains(t, request.body, "<IndexDocument>index.html</IndexDocument>")
	require.Contains(t, request.body, "<ErrorDocument404Path>404.html</ErrorDocument404Path>")
	// Omitted elements keep their current values, so CORS, logging, and metrics must not be sent.
	require.NotContains(t, request.body, "Cors")
	require.NotContains(t, request.body, "Logging")
	require.NotContains(t, request.body, "Metrics")
}

func Test_StaticWebsiteClient_EnsureWebContainer_AlreadyExists(t *testing.T) {
	t.Parallel()

	client, requests := newStaticWebsiteTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-ms-error-code", "ContainerAlreadyExists")
		w.WriteHeader(http.StatusConflict)
	})

	require.NoError(t, client.EnsureWebContainer(t.Context()))
	require.Equal(t, "/$web", (*requests)[0].path)
}

func Test_StaticWebsiteClient_UploadFile_SetsContentType(t *testing.T) {
	t.Parallel()

	client, requests := newStaticWebsiteTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	filePath := filepath.Join(t.TempDir(), "index.html")
	require.NoError(t, os.WriteFile(filePath, []byte("<html></html>"), 0600))

	require.NoError(t, client.UploadFile(t.Context(), "docs/index.html", filePath, "text/html; charset=utf-8"))
	require.Len(t, *requests, 1)
	require.Equal(t, "/$web/docs/index.html", (*requests)[0].path)
	require.Equal(t, "text/html; charset=utf-8", (*requests)[0].header.Get("x-ms-blob-content-type"))
	require.Equal(t, "<html></html>", (*requests)[0].body)
}

func Test_StaticWebsiteClient_DeleteBlob_IgnoresNotFound(t *testing.T) {
	t.Parallel()

	client, _ := newStaticWebsiteTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-ms-error-code", "BlobNotFound")
		w.WriteHeader(http.StatusNotFound)
	})

	require.NoError(t, client.DeleteBlob(t.Context(), "gone.html"))
}

func Test_IsAuthorizationError(t *testing.T) {
	t.Parallel()

	require.True(t, IsAuthorizationError(&azcore.ResponseError{StatusCode: http.StatusForbidden}))
	require.True(t, IsAuthorizationError(&azcore.ResponseError{ErrorCode: "AuthorizationPermissionMismatch"}))
	require.False(t, IsAuthorizationError(&azcore.ResponseError{StatusCode: http.StatusNotFound}))
	require.False(t, IsAuthorizationError(io.EOF))
}
