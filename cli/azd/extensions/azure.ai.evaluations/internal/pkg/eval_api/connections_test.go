// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListConnectionsReadsEveryPage(t *testing.T) {
	const apiVersion = "2025-11-15-preview"
	var hits int32
	client, _ := clientAndServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		assert.Equal(t, "/connections", r.URL.Path)
		assert.Equal(t, apiVersion, r.URL.Query().Get("api-version"))
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"value":[{"name":"second","type":"AzureOpenAI"}]}`)
			return
		}
		fmt.Fprint(w,
			`{"value":[{"name":"first","type":"AzureOpenAI"}],`+
				`"nextLink":"/connections?page=2\u0026api-version=2025-11-15-preview"}`)
	})

	got, err := client.ListConnections(t.Context(), apiVersion)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, []Connection{
		{Name: "first", Type: "AzureOpenAI"},
		{Name: "second", Type: "AzureOpenAI"},
	}, got.Value)
	assert.Empty(t, got.NextLink)
	assert.Equal(t, int32(2), atomic.LoadInt32(&hits))
}

func TestListConnectionsDoesNotReturnAPartialCatalog(t *testing.T) {
	const apiVersion = "2025-11-15-preview"
	client, _ := clientAndServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			http.Error(w, `{"error":{"message":"catalog unavailable"}}`, http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"value":[{"name":"first","type":"AzureOpenAI"}],"nextLink":"/connections?page=2"}`)
	})

	got, err := client.ListConnections(t.Context(), apiVersion)

	require.Error(t, err)
	assert.Nil(t, got, "a partial catalog must never be usable")
	assert.Contains(t, err.Error(), "catalog unavailable")
}
