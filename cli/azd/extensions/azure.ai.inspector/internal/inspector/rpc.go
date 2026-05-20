// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/cli/browser"
	"github.com/gorilla/websocket"
)

// rpcMessage is the JSON-RPC 2.0 envelope used over the WebSocket.
// Quirks preserved from the Python reference:
//   - Inbound request params arrive as a single-element array (unwrap before dispatch).
//   - Outbound notification params are wrapped in a one-element array.
//   - Outbound request params are a positional array.
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

// rpcSession owns one WebSocket. writeMu enforces gorilla/websocket's
// single-writer requirement.
type rpcSession struct {
	cfg    Config
	conn   *websocket.Conn
	logger *log.Logger

	writeMu sync.Mutex

	streamsMu sync.Mutex
	streams   map[string]context.CancelFunc

	idMu          sync.Mutex
	nextRequestID int

	// rootCtx is cancelled on disconnect to wind down SSE pumps.
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

		// Dispatch off the read loop so streaming methods don't block
		// subsequent client messages (e.g. fetchSSE/cancel).
		go sess.handleMessage(&msg)
	}
}

func (s *rpcSession) handleMessage(msg *rpcMessage) {
	if len(msg.ID) == 0 {
		s.handleNotification(msg.Method, msg.Params)
		return
	}

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
		// TODO(inspector): hardcoded for preview; detect from prefers-color-scheme later.
		return "light", nil
	case "openUrlInBrowser":
		return s.openUrlInBrowser(params)
	case "sendTelemetry", "getCurrentStep", "getPlatformSettingsRequest", "executeCommand":
		// Intentionally unhandled in azd standalone mode — these target the VS Code
		// extension host. Returning nil keeps the SPA happy without log noise.
		return nil, nil
	default:
		s.logger.Printf("rpc: unhandled method %q", method)
		return nil, nil
	}
}

func (s *rpcSession) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "setViewReady":
		// SPA has mounted; tell it which agent port to target.
		payload := map[string]any{
			"port":           s.cfg.AgentPort,
			"triggeredFrom":  "standalone",
			"sessionId":      s.cfg.SessionID,
			"conversationId": s.cfg.ConversationID,
		}
		if err := s.sendRequest("navigateToStep", "testTool", payload); err != nil {
			s.logger.Printf("send navigateToStep: %v", err)
		}
	case "inspector/fixRequested":
		s.handleFixRequested(params)
	}
}

// handleFixRequested logs the user's intent to get AI assistance with an
// error. In VS Code this would launch a Copilot chat; in standalone azd
// mode there is no Copilot, so we surface the request in the CLI as a
// signal that the user wanted help. Written directly to stderr (in
// addition to the regular logger) so it always shows in the terminal,
// regardless of whether --debug redirects log.Default() to a file.
func (s *rpcSession) handleFixRequested(raw json.RawMessage) {
	var p struct {
		Source       string `json:"source"`
		ErrorSummary string `json:"errorSummary"`
	}
	if err := json.Unmarshal(unwrapSingleArray(raw), &p); err != nil {
		s.logger.Printf("fixRequested: bad params: %v", err)
		return
	}
	summary := strings.TrimSpace(p.ErrorSummary)
	if summary == "" {
		summary = "(no error details provided)"
	}
	// Collapse newlines/tabs/runs of spaces so the summary fits on one line.
	summary = strings.Join(strings.Fields(summary), " ")
	fmt.Fprintln(os.Stderr, "[inspector] [fix-with-ai] "+summary)
	s.logger.Printf("fix-with-ai: %s", summary)
}

// openUrlInBrowser opens the given URL in the user's default browser.
// Used by the SPA for OAuth-consent links and external doc links.
func (s *rpcSession) openUrlInBrowser(raw json.RawMessage) (any, error) {
	var url string
	if err := json.Unmarshal(raw, &url); err != nil {
		return nil, fmt.Errorf("openUrlInBrowser: bad params: %w", err)
	}
	if url == "" {
		return nil, fmt.Errorf("openUrlInBrowser: empty url")
	}
	if err := browser.OpenURL(url); err != nil {
		s.logger.Printf("openUrlInBrowser: %v", err)
		return nil, err
	}
	return nil, nil
}

func (s *rpcSession) sendResult(id json.RawMessage, result any) {
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
			"code":    -32603,
			"message": err.Error(),
		},
	})
}

// sendNotification wraps params in a one-element array to match
// vscode-jsonrpc's NotificationType1 convention.
func (s *rpcSession) sendNotification(method string, payload any) {
	s.sendRaw(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  []any{payload},
	})
}

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

// unwrapSingleArray converts `[obj]` → `obj`, matching vscode-jsonrpc's
// RequestType1 wire format.
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
