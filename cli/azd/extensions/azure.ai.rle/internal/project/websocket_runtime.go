// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/gorilla/websocket"
)

const (
	maxWebSocketMessageBytes = 100 * 1024 * 1024
	webSocketPingInterval    = 20 * time.Second
	webSocketPingTimeout     = 20 * time.Second
	webSocketDrainTimeout    = 60 * time.Second
)

type WebSocketRuntimeSession struct {
	baseURL               string
	timeout               int
	authorizationProvider AuthorizationProvider
	mu                    sync.Mutex
	exchangeMu            sync.Mutex
	connection            *websocket.Conn
	connectionDone        chan struct{}
	keepAliveInterval     time.Duration
	drainTimeout          time.Duration
	terminalError         error
	closed                bool
}

func NewWebSocketRuntimeSession(
	baseURL string,
	timeout int,
	authorizationProvider AuthorizationProvider,
) *WebSocketRuntimeSession {
	return &WebSocketRuntimeSession{
		baseURL:               baseURL,
		timeout:               timeout,
		authorizationProvider: authorizationProvider,
		keepAliveInterval:     webSocketPingInterval,
		drainTimeout:          webSocketDrainTimeout,
	}
}

// RunWebSocketShellWithContextAndAuthorizationProvider uses one persistent WebSocket
// for stateful OpenEnv operations while retaining the safe HTTP endpoints.
func RunWebSocketShellWithContextAndAuthorizationProvider(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	baseURL string,
	timeout int,
	authorizationProvider AuthorizationProvider,
) error {
	session := NewWebSocketRuntimeSession(baseURL, timeout, authorizationProvider)
	defer session.Close()
	return RunWebSocketShellWithSession(ctx, input, output, session)
}

func RunWebSocketShellWithSession(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	session *WebSocketRuntimeSession,
) error {
	done := make(chan error, 1)
	go func() {
		done <- runShellWithCaller(
			ctx,
			input,
			output,
			session.baseURL,
			session.timeout,
			session.authorizationProvider,
			session.call,
		)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		session.Close()
		fmt.Fprintln(output)
		return nil
	}
}

func (c *WebSocketRuntimeSession) call(
	ctx context.Context,
	baseURL string,
	operation string,
	flags *callOptions,
	authorizationProvider AuthorizationProvider,
) (string, error) {
	switch operation {
	case "reset", "step", "state":
		return c.exchange(ctx, operation, flags, true)
	default:
		return call(ctx, baseURL, operation, flags, authorizationProvider)
	}
}

func (c *WebSocketRuntimeSession) Call(
	ctx context.Context,
	operation string,
	payload string,
) (string, error) {
	flags := &callOptions{timeout: c.timeout}
	switch operation {
	case "reset":
		flags.body = payload
	case "step":
		flags.action = payload
	}
	return c.call(ctx, c.baseURL, operation, flags, c.authorizationProvider)
}

// CallAndDrain preserves cancellation until a request is sent, then drains its response
// so a disconnected HTTP client cannot desynchronize the shared WebSocket session.
func (c *WebSocketRuntimeSession) CallAndDrain(
	ctx context.Context,
	operation string,
	payload string,
) (string, error) {
	flags := &callOptions{timeout: c.timeout}
	switch operation {
	case "reset":
		flags.body = payload
	case "step":
		flags.action = payload
	}
	return c.exchange(ctx, operation, flags, false)
}

func (c *WebSocketRuntimeSession) exchange(
	ctx context.Context,
	operation string,
	flags *callOptions,
	cancelAfterSend bool,
) (string, error) {
	c.exchangeMu.Lock()
	defer c.exchangeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := c.connect(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	connection := c.connection
	terminalError := c.terminalError
	c.mu.Unlock()
	if connection == nil {
		if terminalError != nil {
			return "", terminalError
		}
		return "", fmt.Errorf("OpenEnv WebSocket session closed")
	}

	request, err := webSocketRequest(operation, flags)
	if err != nil {
		return "", err
	}

	deadline, hasDeadline := operationDeadline(ctx, c.timeout)
	if !cancelAfterSend && !hasDeadline {
		deadline = time.Now().Add(c.drainTimeout)
		hasDeadline = true
	}
	if hasDeadline {
		if err := connection.SetWriteDeadline(deadline); err != nil {
			return "", c.failConnection(connection, fmt.Errorf("set OpenEnv WebSocket write deadline: %w", err))
		}
		if err := connection.SetReadDeadline(deadline); err != nil {
			return "", c.failConnection(connection, fmt.Errorf("set OpenEnv WebSocket read deadline: %w", err))
		}
	} else {
		_ = connection.SetWriteDeadline(time.Time{})
		_ = connection.SetReadDeadline(time.Time{})
	}

	if err := connection.WriteMessage(websocket.TextMessage, request); err != nil {
		return "", c.failConnection(
			connection,
			fmt.Errorf("send OpenEnv WebSocket %s request: %w", operation, err),
		)
	}
	var exchangeDone chan struct{}
	var cancellationHandled chan struct{}
	if cancelAfterSend {
		exchangeDone = make(chan struct{})
		cancellationHandled = make(chan struct{})
		go func() {
			select {
			case <-exchangeDone:
			case <-ctx.Done():
				_ = c.failConnection(
					connection,
					fmt.Errorf("OpenEnv WebSocket %s request canceled: %w", operation, ctx.Err()),
				)
			}
			close(cancellationHandled)
		}()
	}
	messageType, response, err := connection.ReadMessage()
	if cancelAfterSend {
		close(exchangeDone)
		<-cancellationHandled
	}
	if err != nil {
		return "", c.failConnection(
			connection,
			fmt.Errorf("receive OpenEnv WebSocket %s response: %w", operation, err),
		)
	}
	if messageType != websocket.TextMessage {
		err := &azdext.LocalError{
			Message:  "Environment runtime returned a non-text WebSocket response.",
			Code:     "rle_open_env_websocket_protocol_error",
			Category: azdext.LocalErrorCategoryInternal,
		}
		return "", c.failConnection(connection, err)
	}
	result, terminal, err := parseWebSocketResponse(operation, response)
	if terminal {
		return "", c.failConnection(connection, err)
	}
	return result, err
}

func (c *WebSocketRuntimeSession) connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.terminalError
	}
	if c.terminalError != nil {
		return c.terminalError
	}
	if c.connection != nil {
		return nil
	}

	endpoint, err := RuntimeWebSocketURL(c.baseURL)
	if err != nil {
		return err
	}
	headers := http.Header{}
	if c.authorizationProvider != nil {
		authorization, err := c.authorizationProvider(ctx)
		if err != nil {
			return fmt.Errorf("authenticate to environment runtime: %w", err)
		}
		if authorization != "" {
			headers.Set("Authorization", authorization)
		}
	}

	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 0
	if c.timeout > 0 {
		dialer.HandshakeTimeout = time.Duration(c.timeout) * time.Second
	}
	connection, response, err := dialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		detail := ""
		if response != nil {
			detail = readHealthErrorDetail(response.Body)
			_ = response.Body.Close()
		}
		return &azdext.LocalError{
			Message: fmt.Sprintf(
				"Environment runtime WebSocket connection failed%s: %v",
				detail,
				err,
			),
			Code:       "rle_open_env_websocket_connection_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Check the remote RLE instance status and retry invoke.",
		}
	}
	connection.SetReadLimit(maxWebSocketMessageBytes)
	c.connection = connection
	c.connectionDone = make(chan struct{})
	go c.keepAlive(connection, c.connectionDone)
	return nil
}

func (c *WebSocketRuntimeSession) keepAlive(connection *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(c.keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			deadline := time.Now().Add(webSocketPingTimeout)
			if err := connection.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				_ = c.failConnection(connection, fmt.Errorf("send OpenEnv WebSocket keepalive: %w", err))
				return
			}
		}
	}
}

func (c *WebSocketRuntimeSession) failConnection(connection *websocket.Conn, err error) error {
	c.mu.Lock()
	if c.connection == connection {
		c.connection = nil
		c.stopKeepAliveLocked()
	}
	if c.terminalError == nil {
		c.terminalError = &azdext.LocalError{
			Message:    fmt.Sprintf("The OpenEnv WebSocket session is no longer usable: %v", err),
			Code:       "rle_open_env_websocket_session_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Exit and run invoke again to start a new environment session.",
		}
	}
	terminalError := c.terminalError
	c.mu.Unlock()
	_ = connection.Close()
	return terminalError
}

func (c *WebSocketRuntimeSession) Close() {
	c.mu.Lock()
	connection := c.connection
	c.connection = nil
	c.stopKeepAliveLocked()
	c.closed = true
	if c.terminalError == nil {
		c.terminalError = &azdext.LocalError{
			Message:    "The OpenEnv WebSocket session is closed.",
			Code:       "rle_open_env_websocket_session_closed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run invoke again to start a new environment session.",
		}
	}
	c.mu.Unlock()
	if connection == nil {
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	_ = connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "RLE invoke complete."),
		deadline,
	)
	_ = connection.Close()
}

func (c *WebSocketRuntimeSession) stopKeepAliveLocked() {
	if c.connectionDone != nil {
		close(c.connectionDone)
		c.connectionDone = nil
	}
}

func parseWebSocketResponse(operation string, response []byte) (string, bool, error) {
	var envelope struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return "", true, &azdext.LocalError{
			Message:  fmt.Sprintf("Environment runtime returned invalid WebSocket JSON: %v", err),
			Code:     "rle_open_env_websocket_protocol_error",
			Category: azdext.LocalErrorCategoryInternal,
		}
	}
	if envelope.Type == "error" {
		return "", false, &azdext.LocalError{
			Message:    fmt.Sprintf("Environment runtime rejected the %s request: %s", operation, prettyJson(envelope.Data)),
			Code:       "rle_open_env_websocket_request_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Check the request payload and retry.",
		}
	}
	expectedType := "observation"
	if operation == "state" {
		expectedType = "state"
	}
	if envelope.Type != expectedType || len(envelope.Data) == 0 {
		return "", true, &azdext.LocalError{
			Message: fmt.Sprintf(
				"Environment runtime returned WebSocket response type %q for %s; expected %q.",
				envelope.Type,
				operation,
				expectedType,
			),
			Code:     "rle_open_env_websocket_protocol_error",
			Category: azdext.LocalErrorCategoryInternal,
		}
	}
	return prettyJson(envelope.Data), false, nil
}

func RuntimeWebSocketURL(baseURL string) (string, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse environment runtime URL: %w", err)
	}
	switch strings.ToLower(endpoint.Scheme) {
	case "http":
		endpoint.Scheme = "ws"
	case "https":
		endpoint.Scheme = "wss"
	default:
		return "", fmt.Errorf("environment runtime URL must use HTTP or HTTPS")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/ws"
	endpoint.RawPath = ""
	return endpoint.String(), nil
}

func webSocketRequest(operation string, flags *callOptions) ([]byte, error) {
	request := map[string]any{"type": operation}
	switch operation {
	case "reset":
		data, err := webSocketData(flags.body, "body", true)
		if err != nil {
			return nil, err
		}
		request["data"] = data
	case "step":
		data, err := webSocketData(flags.action, "action", false)
		if err != nil {
			return nil, err
		}
		request["data"] = data
	case "state":
	default:
		return nil, fmt.Errorf("operation %q is not supported over OpenEnv WebSocket", operation)
	}
	return json.Marshal(request)
}

func webSocketData(value string, flagName string, allowEmpty bool) (map[string]any, error) {
	if strings.TrimSpace(value) == "" && allowEmpty {
		return map[string]any{}, nil
	}
	data, err := validateJsonObject(value, flagName)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func operationDeadline(ctx context.Context, timeoutSeconds int) (time.Time, bool) {
	var deadline time.Time
	hasDeadline := false
	if timeoutSeconds > 0 {
		deadline = time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
		hasDeadline = true
	}
	if contextDeadline, ok := ctx.Deadline(); ok && (!hasDeadline || contextDeadline.Before(deadline)) {
		deadline = contextDeadline
		hasDeadline = true
	}
	return deadline, hasDeadline
}
