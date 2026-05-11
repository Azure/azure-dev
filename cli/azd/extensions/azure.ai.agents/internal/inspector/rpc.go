// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// rpcMessage is the shared envelope for JSON-RPC 2.0 over a single
// WebSocket. The Python reference (rpc_handler.py) emits both requests
// and notifications with this shape; vscode-jsonrpc on the client
// interprets the absence of `id` as a notification.
//
// Important quirks to preserve from the reference:
//   - Inbound request `params` arrive as a single-element array. Unwrap
//     before dispatch.
//   - Outbound notification `params` are wrapped in a one-element array
//     so vscode-jsonrpc's NotificationType handlers see a single arg.
//   - Outbound request `params` are a positional array.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcSession owns a single WebSocket connection. All methods that send
// frames acquire writeMu; gorilla/websocket requires single-writer
// discipline.
type rpcSession struct {
	cfg    Config
	conn   *websocket.Conn
	logger interface{ Printf(string, ...any) }

	writeMu sync.Mutex

	// streams tracks active SSE pumps so that fetchSSE/cancel and
	// connection teardown can stop them cleanly.
	streamsMu sync.Mutex
	streams   map[string]context.CancelFunc

	// nextRequestID seeds server→client request IDs. The Python
	// reference starts at 1 and increments per outgoing request.
	idMu          sync.Mutex
	nextRequestID int

	// rootCtx is cancelled when the WebSocket disconnects so that all
	// in-flight goroutines (SSE pumps) can wind down.
	rootCtx    context.Context
	rootCancel context.CancelFunc
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Printf("ws upgrade failed: %v", err)
		return
	}
	rootCtx, rootCancel := context.WithCancel(r.Context())
	sess := &rpcSession{
		cfg:           s.cfg,
		conn:          conn,
		logger:        s.logger,
		streams:       make(map[string]context.CancelFunc),
		nextRequestID: 1,
		rootCtx:       rootCtx,
		rootCancel:    rootCancel,
	}
	defer sess.cleanup()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.logger.Printf("ws read: %v", err)
			}
			return
		}

		var msg rpcMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			s.logger.Printf("ws decode: %v", err)
			continue
		}

		// Fire-and-forget: streaming methods would block the read loop
		// otherwise and we'd miss subsequent client messages (incl.
		// fetchSSE/cancel).
		go sess.handleMessage(&msg)
	}
}

func (s *rpcSession) handleMessage(msg *rpcMessage) {
	// Notification: no id, no response required.
	if len(msg.ID) == 0 {
		s.handleNotification(msg.Method, msg.Params)
		return
	}

	// Unwrap a single-element array, matching vscode-jsonrpc's
	// RequestType1 convention used throughout the reference handler.
	param := unwrapSingleArray(msg.Params)

	result, err := s.route(msg.Method, param)
	if err != nil {
		s.sendError(msg.ID, err)
		return
	}
	s.sendResult(msg.ID, result)
}

func (s *rpcSession) route(method string, params json.RawMessage) (any, error) {
	switch method {
	case "webviewProxy/fetch":
		return s.proxyFetch(params)
	case "webviewProxy/invoke":
		return s.proxyInvoke(params)
	case "webviewProxy/fetchSSE":
		s.proxyFetchSSE(params)
		return nil, nil
	case "webviewProxy/fetchSSE/cancel":
		s.cancelStream(params)
		return nil, nil
	case "getThemeRequest":
		// TODO(inspector): theme is hardcoded for the preview. Detect
		// from the OS / browser (prefers-color-scheme on the SPA side)
		// or expose a CLI flag once the inspector ships GA.
		return "light", nil
	default:
		// Unimplemented methods (including webviewProxy/ws/*, which the
		// SPA does not currently use against the standalone backend)
		// fall through to null, matching the Python reference's default.
		return nil, nil
	}
}

func (s *rpcSession) handleNotification(method string, params json.RawMessage) {
	if method == "setViewReady" {
		// The SPA signals it has mounted; reply with a navigateToStep
		// request carrying the agent port so the inspector knows where
		// to send proxied calls.
		payload := map[string]any{
			"port":          s.cfg.AgentPort,
			"triggeredFrom": "standalone",
		}
		if err := s.sendRequest("navigateToStep", "testTool", payload); err != nil {
			s.logger.Printf("send navigateToStep: %v", err)
		}
	}
}

// ──────────────────────────── outbound ────────────────────────────

func (s *rpcSession) sendResult(id json.RawMessage, result any) {
	if result == nil {
		// JSON-RPC requires the result key to be present. Use an
		// explicit JSON null to keep the wire shape consistent.
		s.sendRaw(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(id),
			"result":  nil,
		})
		return
	}
	s.sendRaw(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"result":  result,
	})
}

func (s *rpcSession) sendError(id json.RawMessage, err error) {
	s.sendRaw(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"error": map[string]any{
			"code":    -1,
			"message": fmt.Sprintf("%T: %v", err, err),
		},
	})
}

// sendNotification matches rpc_handler.py: params is wrapped in a
// one-element array so vscode-jsonrpc's NotificationType1 handlers see
// the object as their first (and only) argument.
func (s *rpcSession) sendNotification(method string, payload any) {
	s.sendRaw(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  []any{payload},
	})
}

// sendRequest sends a server→client request with a fresh id. params
// are positional, matching vscode-jsonrpc RequestType conventions.
// The current implementation does not await responses; the inspector
// does not return values for the requests we send (e.g. navigateToStep).
func (s *rpcSession) sendRequest(method string, params ...any) error {
	s.idMu.Lock()
	id := s.nextRequestID
	s.nextRequestID++
	s.idMu.Unlock()

	return s.sendRaw(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
}

func (s *rpcSession) sendRaw(payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		s.logger.Printf("rpc marshal: %v", err)
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		s.logger.Printf("rpc write: %v", err)
		return err
	}
	return nil
}

// ──────────────────────────── lifecycle ────────────────────────────

func (s *rpcSession) registerStream(id string, cancel context.CancelFunc) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	s.streams[id] = cancel
}

func (s *rpcSession) unregisterStream(id string) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	delete(s.streams, id)
}

func (s *rpcSession) cancelStream(params json.RawMessage) {
	// fetchSSE/cancel arrives as the bare requestId string (positional
	// arg unwrapped from the single-element array).
	var id string
	if err := json.Unmarshal(params, &id); err != nil {
		s.logger.Printf("cancel: bad params: %v", err)
		return
	}
	s.streamsMu.Lock()
	cancel, ok := s.streams[id]
	delete(s.streams, id)
	s.streamsMu.Unlock()
	if ok {
		cancel()
	}
}

func (s *rpcSession) cleanup() {
	s.rootCancel()

	s.streamsMu.Lock()
	for _, cancel := range s.streams {
		cancel()
	}
	s.streams = nil
	s.streamsMu.Unlock()

	_ = s.conn.Close()
}

// ──────────────────────────── helpers ────────────────────────────

// unwrapSingleArray converts `[obj]` → `obj`. vscode-jsonrpc sends
// RequestType1 params wrapped in a one-element array; without this we
// would have to teach every handler the wrapping convention.
func unwrapSingleArray(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) == 1 {
		return arr[0]
	}
	return raw
}
