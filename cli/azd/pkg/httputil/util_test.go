// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package httputil

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type exampleResponse struct {
	A string `json:"a"`
	B string `json:"b"`
	C string `json:"c"`
}

func TestTunedTransport(t *testing.T) {
	transport := TunedTransport()

	require.NotNil(t, transport)
	require.Equal(t, 200, transport.MaxIdleConns)
	require.Equal(t, 50, transport.MaxConnsPerHost)
	require.Equal(t, 50, transport.MaxIdleConnsPerHost)
	require.Equal(t, 90*time.Second, transport.IdleConnTimeout)
	require.False(t, transport.DisableKeepAlives)
}

func TestTunedTransport_DoesNotMutateDefault(t *testing.T) {
	defaultTransport := http.DefaultTransport.(*http.Transport)
	origMaxIdle := defaultTransport.MaxIdleConns
	origMaxConns := defaultTransport.MaxConnsPerHost
	origMaxIdlePerHost := defaultTransport.MaxIdleConnsPerHost
	origIdleTimeout := defaultTransport.IdleConnTimeout
	origKeepAlives := defaultTransport.DisableKeepAlives

	_ = TunedTransport()

	// Verify no field of the default transport was mutated.
	require.Equal(t, origMaxIdle, defaultTransport.MaxIdleConns)
	require.Equal(t, origMaxConns, defaultTransport.MaxConnsPerHost)
	require.Equal(t, origMaxIdlePerHost, defaultTransport.MaxIdleConnsPerHost)
	require.Equal(t, origIdleTimeout, defaultTransport.IdleConnTimeout)
	require.Equal(t, origKeepAlives, defaultTransport.DisableKeepAlives)
}

func TestReadRawResponse(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expectedResponse := &exampleResponse{
			A: "Apple",
			B: "Banana",
			C: "Carrot",
		}

		jsonBytes, err := json.Marshal(expectedResponse)
		require.NoError(t, err)

		httpResponse := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}

		actualResponse, err := ReadRawResponse[exampleResponse](httpResponse)
		require.NoError(t, err)
		require.Equal(t, *expectedResponse, *actualResponse)
	})
}
