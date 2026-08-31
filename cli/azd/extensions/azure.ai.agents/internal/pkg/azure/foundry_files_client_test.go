// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestFilesClient builds a FoundryFilesClient backed by a custom
// round-tripper so request shapes can be asserted without the network.
func newTestFilesClient(endpoint string, fn roundTripFunc) *FoundryFilesClient {
	return &FoundryFilesClient{
		endpoint: endpoint,
		pipeline: newTestPipeline(fn),
	}
}

func TestUploadFile_RequestShape(t *testing.T) {
	var captured *http.Request
	var body []byte

	client := newTestFilesClient("https://proj.example.com", func(req *http.Request) (*http.Response, error) {
		captured = req
		if req.Body != nil {
			body, _ = io.ReadAll(req.Body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"file-1","filename":"faq.md","purpose":"assistants"}`)),
			Header:     make(http.Header),
		}, nil
	})

	obj, err := client.UploadFile(t.Context(), "faq.md", []byte("hello world"), "")
	require.NoError(t, err)
	require.Equal(t, "file-1", obj.Id)

	require.NotNil(t, captured)
	require.Equal(t, http.MethodPost, captured.Method)
	require.Equal(t, "/openai/v1/files", captured.URL.EscapedPath())
	require.Empty(t, captured.URL.RawQuery)
	require.Contains(t, captured.Header.Get("Content-Type"), "multipart/form-data")

	// The multipart body should carry the filename, the content, and the
	// default purpose ("assistants") when none was supplied.
	bodyStr := string(body)
	require.Contains(t, bodyStr, "faq.md")
	require.Contains(t, bodyStr, "hello world")
	require.Contains(t, bodyStr, "assistants")
}

func TestCreateVectorStore_RequestShape(t *testing.T) {
	var captured *http.Request
	var body []byte

	client := newTestFilesClient("https://proj.example.com", func(req *http.Request) (*http.Response, error) {
		captured = req
		if req.Body != nil {
			body, _ = io.ReadAll(req.Body)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`{"id":"vs-1","name":"agent","object":"vector_store"}`)),
			Header:     make(http.Header),
		}, nil
	})

	store, err := client.CreateVectorStore(t.Context(), "agent", []string{"file-1", "file-2"})
	require.NoError(t, err)
	require.Equal(t, "vs-1", store.Id)

	require.NotNil(t, captured)
	require.Equal(t, http.MethodPost, captured.Method)
	require.Equal(t, "/openai/v1/vector_stores", captured.URL.EscapedPath())
	require.Equal(t, "application/json", captured.Header.Get("Content-Type"))

	bodyStr := string(body)
	require.Contains(t, bodyStr, `"name":"agent"`)
	require.Contains(t, bodyStr, `"file-1"`)
	require.Contains(t, bodyStr, `"file-2"`)
}

func TestUploadFile_ErrorStatus(t *testing.T) {
	client := newTestFilesClient("https://proj.example.com", func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`{"error":"nope"}`)),
			Header:     make(http.Header),
		}, nil
	})

	_, err := client.UploadFile(t.Context(), "faq.md", []byte("x"), "assistants")
	require.Error(t, err)
}
