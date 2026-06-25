// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
)

func TestWsStream_Close_ReturnsNil(t *testing.T) {
	// wsStream.Close is a no-op that returns nil.
	// See TODO in stream.go referencing issue #3286.
	s := wsStream{}
	err := s.Close()
	assert.NoError(t, err)
}

func TestWsStream_Close(t *testing.T) {
	s := wsStream{}
	require.NoError(t, s.Close())
}

func TestNewWebSocketStream(t *testing.T) {
	ws := newWebSocketStream(nil)
	require.NotNil(t, ws)
	require.Nil(t, ws.c)
}

func TestWsStream_ReadWrite(t *testing.T) {
	// Create an httptest server that upgrades to WebSocket and echoes back the RPC messages.
	echoHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		defer c.Close()

		stream := newWebSocketStream(c)

		// Read a message and write it back
		msg, _, err := stream.Read(t.Context())
		if err != nil {
			t.Logf("read error: %v", err)
			return
		}

		_, err = stream.Write(t.Context(), msg)
		if err != nil {
			t.Logf("write error: %v", err)
		}
	})

	server := httptest.NewServer(echoHandler)
	defer server.Close()

	// Connect via WebSocket
	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	serverURL.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverURL.String(), nil)
	require.NoError(t, err)
	defer wsConn.Close()

	stream := newWebSocketStream(wsConn)

	// Send a JSON-RPC notification
	notification, err := jsonrpc2.NewNotification("test/method", []string{"arg1"})
	require.NoError(t, err)

	n, err := stream.Write(t.Context(), notification)
	require.NoError(t, err)
	require.Greater(t, n, int64(0))

	// Read it back (the server echoes it)
	msg, bytesRead, err := stream.Read(t.Context())
	require.NoError(t, err)
	require.Greater(t, bytesRead, int64(0))
	require.NotNil(t, msg)
}

func TestWsStream_Read_NonTextMessage(t *testing.T) {
	// Server sends a binary message, which should cause Read to error
	binaryHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		// Send a binary message
		_ = c.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03})
	})

	server := httptest.NewServer(binaryHandler)
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	serverURL.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverURL.String(), nil)
	require.NoError(t, err)
	defer wsConn.Close()

	stream := newWebSocketStream(wsConn)

	_, _, err = stream.Read(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected message type")
}

func connectRPC(t *testing.T, handler http.Handler) jsonrpc2.Conn {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	serverURL.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverURL.String(), nil)
	require.NoError(t, err)

	rpcConn := jsonrpc2.NewConn(newWebSocketStream(wsConn))
	rpcConn.Go(t.Context(), nil)
	return rpcConn
}
