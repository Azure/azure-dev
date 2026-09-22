// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/stretchr/testify/require"
)

type stateStoreErrorTransport struct {
	err      error
	requests []*http.Request
}

func (t *stateStoreErrorTransport) Do(req *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, req)
	return nil, t.err
}

func TestStateStoreRequests(t *testing.T) {
	store, key := "checkpoints/作業 %?#", "task/one ?#%"
	base := "/api/projects/proj/agents/worker/endpoint/state_stores"
	storePath := base + "/" + base64.RawURLEncoding.EncodeToString([]byte(store))
	itemPath := storePath + "/items/" + base64.RawURLEncoding.EncodeToString([]byte(key))
	page := StateStoreListOptions{Limit: 2, Order: "asc", After: "opaque+/= cursor"}
	value := SetStateStoreItemRequest{
		Value: json.RawMessage(`{"number":9007199254740993,"nested":[null,true,"text"]}`),
		Tags:  map[string]string{"kind": "checkpoint"},
	}
	for _, tt := range []struct {
		name, method, path string
		status             int
		list, conditional  bool
		call               func(*AgentClient) (any, error)
	}{
		{"list stores", "GET", base, 200, true, false, func(c *AgentClient) (any, error) {
			return c.ListStateStores(t.Context(), "worker", page)
		}},
		{"get store", "GET", storePath, 200, false, false, func(c *AgentClient) (any, error) {
			return c.GetStateStore(t.Context(), "worker", store)
		}},
		{"list keys", "GET", storePath + "/items:keys", 200, true, false, func(c *AgentClient) (any, error) {
			return c.ListStateStoreItemKeys(t.Context(), "worker", store, page)
		}},
		{"get item", "GET", itemPath, 200, false, false, func(c *AgentClient) (any, error) {
			return c.GetStateStoreItem(t.Context(), "worker", store, key)
		}},
		{"replace item", "PUT", itemPath, 200, false, true, func(c *AgentClient) (any, error) {
			return c.SetStateStoreItem(t.Context(), "worker", store, key, value, `"etag"`)
		}},
		{"create item", "PUT", itemPath, 201, false, false, func(c *AgentClient) (any, error) {
			return c.SetStateStoreItem(t.Context(), "worker", store, key, value, "")
		}},
		{"delete item", "DELETE", itemPath, 200, false, true, func(c *AgentClient) (any, error) {
			return c.DeleteStateStoreItem(t.Context(), "worker", store, key, `"etag"`)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, transport := newCaptureClient(tt.status, `{}`)
			client.endpoint += "/"
			_, err := tt.call(client)
			require.NoError(t, err)
			require.Len(t, transport.requests, 1)
			req := transport.requests[0]
			require.Equal(t, tt.method, req.Method)
			require.Equal(t, tt.path, req.URL.EscapedPath())
			require.Equal(t, "v1", req.URL.Query().Get("api-version"))
			require.Equal(t, stateStoresPreviewFeature, req.Header.Get("Foundry-Features"))
			require.Empty(t, req.Header.Get(UserIdentityHeader))
			if tt.conditional {
				require.Equal(t, `"etag"`, req.Header.Get("If-Match"))
			} else {
				require.Empty(t, req.Header.Get("If-Match"))
			}
			if tt.list {
				require.Equal(t, "2", req.URL.Query().Get("limit"))
				require.Equal(t, "asc", req.URL.Query().Get("order"))
				require.Equal(t, page.After, req.URL.Query().Get("after"))
				require.False(t, req.URL.Query().Has("before"))
			}
			if tt.method == "PUT" {
				data, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.JSONEq(t, `{"value":`+string(value.Value)+`,"tags":{"kind":"checkpoint"}}`, string(data))
				require.Contains(t, string(data), "9007199254740993")
				require.Equal(t, "application/json", req.Header.Get("Content-Type"))
			}
		})
	}
}

func TestStateStoreResponses(t *testing.T) {
	t.Run("get preserves JSON numbers", func(t *testing.T) {
		client, _ := newCaptureClient(200, `{"key":"key","value":{"n":9007199254740993},"etag":"\"e\""}`)
		item, err := client.GetStateStoreItem(t.Context(), "worker", "store", "key")
		require.NoError(t, err)
		require.Equal(t, `{"n":9007199254740993}`, string(item.Value))
		require.Equal(t, `"e"`, item.ETag)
	})
	t.Run("page preserves cursors", func(t *testing.T) {
		client, _ := newCaptureClient(200, `{"data":[{"name":"store"}],"first_id":"f","last_id":"l","has_more":true}`)
		page, err := client.ListStateStores(t.Context(), "worker", StateStoreListOptions{})
		require.NoError(t, err)
		require.Equal(t, "store", page.Data[0].Name)
		require.Equal(t, "f", *page.FirstID)
		require.Equal(t, "l", *page.LastID)
		require.True(t, page.HasMore)
	})
	t.Run("metadata write and omitted tags", func(t *testing.T) {
		client, transport := newCaptureClient(201, `{"key":"key","etag":"\"e\""}`)
		item, err := client.SetStateStoreItem(t.Context(), "worker", "store", "key",
			SetStateStoreItemRequest{Value: json.RawMessage(`{}`)}, "")
		require.NoError(t, err)
		require.Nil(t, item.Value)
		data, err := io.ReadAll(transport.requests[0].Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"value":{},"tags":null}`, string(data))
	})
	t.Run("repeat deletion", func(t *testing.T) {
		client, _ := newCaptureClient(200, `{"id":null,"key":"key","deleted":true}`)
		result, err := client.DeleteStateStoreItem(t.Context(), "worker", "store", "key", "")
		require.NoError(t, err)
		require.Nil(t, result.ID)
		require.True(t, result.Deleted)
	})
	t.Run("malformed response", func(t *testing.T) {
		client, _ := newCaptureClient(200, `not json containing secret value`)
		_, err := client.GetStateStore(t.Context(), "worker", "store")
		require.EqualError(t, err, "invalid state store response JSON")
	})
}

func TestStateStoreValidation(t *testing.T) {
	for _, value := range []string{`null`, `[]`, `"text"`, `1`, `true`, ``, `{"a":}`, `{} {}`} {
		t.Run(value, func(t *testing.T) {
			client, transport := newCaptureClient(200, `{}`)
			_, err := client.SetStateStoreItem(t.Context(), "a", "s", "k",
				SetStateStoreItemRequest{Value: json.RawMessage(value)}, "")
			require.ErrorContains(t, err, "JSON object")
			require.Empty(t, transport.requests)
		})
	}
	for _, options := range []StateStoreListOptions{
		{Limit: -1}, {Limit: 101}, {Order: "invalid"},
	} {
		client, transport := newCaptureClient(200, `{}`)
		_, err := client.ListStateStores(t.Context(), "a", options)
		require.Error(t, err)
		require.Empty(t, transport.requests)
	}
	for _, names := range [][3]string{{"", "s", "k"}, {"a", "", "k"}, {"a", "s", ""}} {
		client, transport := newCaptureClient(200, `{}`)
		_, err := client.GetStateStoreItem(t.Context(), names[0], names[1], names[2])
		require.Error(t, err)
		require.Empty(t, transport.requests)
	}
	for _, order := range []string{"asc", "desc"} {
		t.Run("after cursor unchanged/"+order, func(t *testing.T) {
			client, transport := newCaptureClient(200,
				`{"data":[{"id":"ss-resource-id","name":"store/raw ?%"}],"last_id":"store/raw ?%","has_more":true}`)
			page, err := client.ListStateStores(t.Context(), "a", StateStoreListOptions{Order: order})
			require.NoError(t, err)
			require.NotNil(t, page.LastID)
			require.NotEqual(t, page.Data[0].ID, *page.LastID)
			_, err = client.ListStateStores(t.Context(), "a", StateStoreListOptions{Order: order, After: *page.LastID})
			require.NoError(t, err)
			require.Len(t, transport.requests, 2)
			require.Empty(t, transport.requests[0].URL.Query().Get("after"))
			query := transport.requests[1].URL.Query()
			require.Equal(t, "store/raw ?%", query.Get("after"))
			require.Equal(t, order, query.Get("order"))
			require.Empty(t, query.Get("limit"))
			require.False(t, query.Has("before"))
		})
	}
}

func TestStateStoreWriteOutcomeUnknown(t *testing.T) {
	for _, operation := range []string{"set", "delete"} {
		t.Run(operation+"/transport", func(t *testing.T) {
			transport := &stateStoreErrorTransport{err: io.ErrUnexpectedEOF}
			client := newTestClient("https://test.example.com/api/projects/proj", transport)
			var err error
			if operation == "set" {
				_, err = client.SetStateStoreItem(t.Context(), "agent", "store", "key",
					SetStateStoreItemRequest{Value: json.RawMessage(`{}`)}, `"etag"`)
			} else {
				_, err = client.DeleteStateStoreItem(t.Context(), "agent", "store", "key", `"etag"`)
			}
			unknown, ok := errors.AsType[*StateStoreWriteOutcomeUnknownError](err)
			require.True(t, ok)
			require.ErrorIs(t, unknown, io.ErrUnexpectedEOF)
			require.Len(t, transport.requests, 1, "writes are never retried after an uncertain outcome")
			require.Equal(t, `"etag"`, transport.requests[0].Header.Get("If-Match"))
		})
		t.Run(operation+"/invalid response", func(t *testing.T) {
			client, transport := newCaptureClient(http.StatusOK, `not-json-containing-secret-value`)
			var err error
			if operation == "set" {
				_, err = client.SetStateStoreItem(t.Context(), "agent", "store", "key",
					SetStateStoreItemRequest{Value: json.RawMessage(`{}`)}, "")
			} else {
				_, err = client.DeleteStateStoreItem(t.Context(), "agent", "store", "key", "")
			}
			_, ok := errors.AsType[*StateStoreWriteOutcomeUnknownError](err)
			require.True(t, ok)
			require.EqualError(t, err, "invalid state store response JSON")
			require.NotContains(t, err.Error(), "secret-value")
			require.Len(t, transport.requests, 1)
		})
	}
	t.Run("read transport remains ordinary", func(t *testing.T) {
		transport := &stateStoreErrorTransport{err: io.ErrUnexpectedEOF}
		client := newTestClient("https://test.example.com/api/projects/proj", transport)
		_, err := client.GetStateStoreItem(t.Context(), "agent", "store", "key")
		_, ok := errors.AsType[*StateStoreWriteOutcomeUnknownError](err)
		require.False(t, ok)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	})
}

func TestStateStoreErrorsDoNotExposeBodies(t *testing.T) {
	var messages []string
	log.SetEvents(log.EventRequest, log.EventResponse, log.EventResponseError)
	log.SetListener(func(_ log.Event, message string) { messages = append(messages, message) })
	t.Cleanup(func() { log.SetListener(nil); log.SetEvents() })
	for _, status := range []int{400, 401, 403, 404, 409, 412, 429, 500} {
		client, transport := newCaptureClient(status, `{"error":{"message":"secret-value-xyz"}}`)
		_, err := client.SetStateStoreItem(t.Context(), "a", "s", "k",
			SetStateStoreItemRequest{Value: json.RawMessage(`{"secret":"secret-value-xyz"}`)}, `"etag"`)
		require.Error(t, err)
		respErr, ok := errors.AsType[*azcore.ResponseError](err)
		require.True(t, ok)
		require.Equal(t, status, respErr.StatusCode)
		require.NotContains(t, err.Error(), "secret-value-xyz")
		require.Len(t, transport.requests, 1, "writes must not be replayed")
		require.Equal(t, `"etag"`, transport.requests[0].Header.Get("If-Match"))
	}
	require.NotContains(t, strings.Join(messages, "\n"), "secret-value-xyz")
	// A missing store is not treated as a successful repeated deletion.
	client, _ := newCaptureClient(http.StatusNotFound, `{}`)
	_, err := client.DeleteStateStoreItem(t.Context(), "a", "s", "k", "")
	require.Error(t, err)
}
