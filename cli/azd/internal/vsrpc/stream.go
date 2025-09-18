// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"
	"go.lsp.dev/jsonrpc2"
)

// wsStream adapts the websocket.Conn to jsonrpc2.Stream interface
type wsStream struct {
	c *websocket.Conn
}

// Close implements jsonrpc2.Stream.
func (*wsStream) Close() error {
	// TODO(azure/azure-dev#3286): Need to think about what to do here.  Close the conn?
	return nil
}

// Read implements jsonrpc2.Stream.
func (s *wsStream) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	mt, data, err := s.c.ReadMessage()
	if err != nil {
		return nil, 0, err
	}
	if mt != websocket.TextMessage {
		return nil, 0, fmt.Errorf("unexpected message type: %v", mt)
	}
	msg, err := jsonrpc2.DecodeMessage(data)
	if err != nil {
		return nil, 0, err
	}
	return msg, int64(len(data)), nil
}

// Write implements jsonrpc2.Stream.
func (s *wsStream) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %w", err)
	}

	if err := s.c.WriteMessage(websocket.TextMessage, data); err != nil {
		return 0, err
	}

	return int64(len(data)), nil
}

func newWebSocketStream(c *websocket.Conn) *wsStream {
	return &wsStream{c: c}
}
