// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestMemoryStoreClient creates a FoundryMemoryStoreClient backed by a custom
// HTTP round-tripper so we can inspect requests and control responses without
// touching the network.
func newTestMemoryStoreClient(endpoint string, fn roundTripFunc) *FoundryMemoryStoreClient {
	return &FoundryMemoryStoreClient{
		endpoint: strings.TrimRight(endpoint, "/"),
		pipeline: newTestPipeline(fn),
	}
}

func TestCreateMemoryStore_RequestShape(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte

	client := newTestMemoryStoreClient("https://example.com/api/projects/proj",
		func(req *http.Request) (*http.Response, error) {
			capturedReq = req
			if req.Body != nil {
				capturedBody, _ = io.ReadAll(req.Body)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(strings.NewReader(`{"id":"ms-1","name":"my_store"}`)),
				Request:    req,
			}, nil
		})

	store, err := client.CreateMemoryStore(t.Context(), &CreateMemoryStoreRequest{
		Name:        "my_store",
		Description: "test store",
		Definition: MemoryStoreDefinition{
			Kind:           MemoryStoreKindDefault,
			ChatModel:      "gpt-5.2",
			EmbeddingModel: "text-embedding-3-small",
			Options: &MemoryStoreOptions{
				UserProfileEnabled: new(true),
				UserProfileDetails: "avoid sensitive data",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "ms-1", store.Id)
	require.Equal(t, "my_store", store.Name)

	require.Equal(t, http.MethodPost, capturedReq.Method)
	require.Equal(t, "/api/projects/proj/memory_stores", capturedReq.URL.Path)
	require.Equal(t, memoryStoreAPIVersion, capturedReq.URL.Query().Get("api-version"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(capturedBody, &body))
	require.Equal(t, "my_store", body["name"])
	def := body["definition"].(map[string]any)
	require.Equal(t, "default", def["kind"])
	require.Equal(t, "gpt-5.2", def["chat_model"])
	require.Equal(t, "text-embedding-3-small", def["embedding_model"])
	opts := def["options"].(map[string]any)
	require.Equal(t, true, opts["user_profile_enabled"])
	require.Equal(t, "avoid sensitive data", opts["user_profile_details"])
}

func TestGetMemoryStore_EscapesNameAndParsesResponse(t *testing.T) {
	var capturedReq *http.Request

	client := newTestMemoryStoreClient("https://example.com/api/projects/proj",
		func(req *http.Request) (*http.Response, error) {
			capturedReq = req
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"ms-2","name":"a b"}`)),
				Request:    req,
			}, nil
		})

	store, err := client.GetMemoryStore(t.Context(), "a b")
	require.NoError(t, err)
	require.Equal(t, "ms-2", store.Id)
	require.Equal(t, http.MethodGet, capturedReq.Method)
	require.Equal(t, "/api/projects/proj/memory_stores/a%20b", capturedReq.URL.EscapedPath())
}

func TestEnsureMemoryStore_ExistingStoreIsLeftAsIs(t *testing.T) {
	var methods []string

	client := newTestMemoryStoreClient("https://example.com/api/projects/proj",
		func(req *http.Request) (*http.Response, error) {
			methods = append(methods, req.Method)
			// GET succeeds: store already exists.
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"ms-3","name":"existing"}`)),
				Request:    req,
			}, nil
		})

	store, created, err := client.EnsureMemoryStore(t.Context(), &CreateMemoryStoreRequest{
		Name: "existing",
		Definition: MemoryStoreDefinition{
			Kind:           MemoryStoreKindDefault,
			ChatModel:      "chat",
			EmbeddingModel: "embed",
		},
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "ms-3", store.Id)
	require.Equal(t, []string{http.MethodGet}, methods, "only a GET should be issued for an existing store")
}

func TestEnsureMemoryStore_CreatesWhenMissing(t *testing.T) {
	var methods []string

	client := newTestMemoryStoreClient("https://example.com/api/projects/proj",
		func(req *http.Request) (*http.Response, error) {
			methods = append(methods, req.Method)
			if req.Method == http.MethodGet {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"NotFound"}}`)),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(strings.NewReader(`{"id":"ms-4","name":"new"}`)),
				Request:    req,
			}, nil
		})

	store, created, err := client.EnsureMemoryStore(t.Context(), &CreateMemoryStoreRequest{
		Name: "new",
		Definition: MemoryStoreDefinition{
			Kind:           MemoryStoreKindDefault,
			ChatModel:      "chat",
			EmbeddingModel: "embed",
		},
	})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "ms-4", store.Id)
	require.Equal(t, []string{http.MethodGet, http.MethodPost}, methods)
}

func TestEnsureMemoryStore_PropagatesNon404GetErrors(t *testing.T) {
	var methods []string

	client := newTestMemoryStoreClient("https://example.com/api/projects/proj",
		func(req *http.Request) (*http.Response, error) {
			methods = append(methods, req.Method)
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"Forbidden"}}`)),
				Request:    req,
			}, nil
		})

	_, _, err := client.EnsureMemoryStore(t.Context(), &CreateMemoryStoreRequest{
		Name: "denied",
		Definition: MemoryStoreDefinition{
			Kind:           MemoryStoreKindDefault,
			ChatModel:      "chat",
			EmbeddingModel: "embed",
		},
	})
	require.Error(t, err)
	require.Equal(t, []string{http.MethodGet}, methods, "a non-404 GET error must not trigger a create")
}
