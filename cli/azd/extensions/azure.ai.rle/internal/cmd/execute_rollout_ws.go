// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// executeRolloutSubprotocol is offered in Sec-WebSocket-Protocol and selected by the
// service. An upgrade that does not offer it is rejected before the socket is accepted
// (vienna EntryPoints/Controllers/V1/ExecuteRolloutController.cs ExecuteRolloutWebSocketAsync).
const executeRolloutSubprotocol = "rle.execute-rollout.v1"

// executeRolloutWebSocketGroupID is the reserved, literal instance_groups segment RLE
// publishes the rollout upgrade under. The Foundry data-plane gateway forwards a WebSocket
// upgrade only when the path matches an API registered for the resource, and the rollout
// path is not registered: calling `.../versions/{version}/rollouts/ws` is rejected at the
// gateway with HTTP 400 ("API called azure-ai-projects-websocket-api not matching any APIs
// in resource") and never reaches RLE. The OpenEnv instance template is registered and its
// instance_groups and instances segments are free-form, so RLE publishes the same handler
// under that shape (vienna PR !2317079). Retire this once the rollout path is registered.
const executeRolloutWebSocketGroupID = "_execute_rollout"

const (
	// The gateway terminates an Execute Rollout HTTP request at roughly 120 seconds with a
	// 408, which is shorter than a real Harness rollout. The upgraded socket is not subject
	// to that cut (measured: idle to 180s, then a rollout completing at 206s), so the only
	// deadline that applies here is the caller's context.
	executeRolloutHandshakeTimeout = 30 * time.Second
	executeRolloutWriteTimeout     = 30 * time.Second
	executeRolloutCloseTimeout     = 2 * time.Second

	// A rollout response carries the full captured trajectory, so the frame is large.
	maxExecuteRolloutFrameBytes  = 64 * 1024 * 1024
	maxExecuteRolloutHeaderBytes = 64 * 1024
)

// Frame types exchanged on the socket.
const (
	executeRolloutFrameExecute   = "execute"
	executeRolloutFrameCompleted = "completed"
	executeRolloutFrameError     = "error"
)

// executeRolloutFrameHeader is the JSON header of an application message. The header owns
// rollout_id: the service rejects a payload whose rollout_id disagrees with it.
type executeRolloutFrameHeader struct {
	Type      string `json:"type"`
	RolloutID string `json:"rollout_id"`
}

// dialExecuteRolloutWebSocket is the dial seam. gorilla's Dialer does not run through
// rleClient.httpClient.Transport, so tests replace this rather than the round tripper.
var dialExecuteRolloutWebSocket = func(
	ctx context.Context,
	endpoint string,
	headers http.Header,
) (*websocket.Conn, *http.Response, error) {
	dialer := &websocket.Dialer{
		HandshakeTimeout: executeRolloutHandshakeTimeout,
		Subprotocols:     []string{executeRolloutSubprotocol},
		Proxy:            http.ProxyFromEnvironment,
	}
	return dialer.DialContext(ctx, endpoint, headers)
}

// executeRolloutHandshakeError marks a failure to establish the socket, before any rollout
// was requested. It is the only failure that may be retried on another transport: once the
// execute frame is on the wire the rollout may have run, so a retry could double-execute it.
type executeRolloutHandshakeError struct {
	statusCode int
	body       string
	cause      error
}

func (e *executeRolloutHandshakeError) Error() string {
	if e.statusCode != 0 {
		return fmt.Sprintf("Execute Rollout WebSocket upgrade failed with HTTP %d: %s", e.statusCode, e.body)
	}
	return fmt.Sprintf("Execute Rollout WebSocket upgrade failed: %v", e.cause)
}

func (e *executeRolloutHandshakeError) Unwrap() error { return e.cause }

func newExecuteRolloutHandshakeError(cause error, response *http.Response) *executeRolloutHandshakeError {
	result := &executeRolloutHandshakeError{cause: cause}
	if response == nil {
		return result
	}
	result.statusCode = response.StatusCode
	if response.Body != nil {
		defer response.Body.Close()
		if body, err := io.ReadAll(io.LimitReader(response.Body, maxExecuteRolloutHeaderBytes)); err == nil {
			result.body = strings.TrimSpace(string(body))
		}
	}
	return result
}

// executeRolloutWebSocketURL builds the alias endpoint. The rollout ID doubles as the
// instances segment so service-side logs correlate with the rollout the CLI reports.
func (c *rleClient) executeRolloutWebSocketURL(
	environmentName string,
	environmentVersion string,
	rolloutID string,
) (string, error) {
	path := fmt.Sprintf(
		"%s/%s/versions/%s/instance_groups/%s/instances/%s/openenv/ws",
		environmentCollectionPath,
		url.PathEscape(environmentName),
		url.PathEscape(environmentVersion),
		executeRolloutWebSocketGroupID,
		url.PathEscape(rolloutID),
	)
	endpoint, err := url.Parse(c.baseUrl + path)
	if err != nil {
		return "", fmt.Errorf("create Execute Rollout WebSocket URL: %w", err)
	}
	if !strings.EqualFold(endpoint.Scheme, "https") {
		return "", errors.New("RLE service authentication requires an HTTPS Foundry project endpoint")
	}
	endpoint.Scheme = "wss"
	query := endpoint.Query()
	query.Set("api-version", foundryAPIVersion)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

// executeRolloutOverWebSocket runs one rollout on a dedicated connection. The CLI executes a
// single rollout per invocation, so the socket is opened, used and closed here; the wire
// format supports several concurrent rollouts on one connection, which a batching caller
// would use by keeping the connection and dispatching replies by rollout_id.
func (c *rleClient) executeRolloutOverWebSocket(
	ctx context.Context,
	environmentName string,
	environmentVersion string,
	loomBearerToken string,
	request executeRolloutRequest,
) (*executeRolloutResponse, error) {
	endpoint, err := c.executeRolloutWebSocketURL(environmentName, environmentVersion, request.RolloutID)
	if err != nil {
		return nil, err
	}
	authorization, err := c.authorizationHeader(ctx)
	if err != nil {
		return nil, fmt.Errorf("authenticate to Foundry: %w", err)
	}

	headers := http.Header{}
	headers.Set("Authorization", authorization)
	headers.Set(executeRolloutHeader, loomBearerToken)

	connection, response, err := dialExecuteRolloutWebSocket(ctx, endpoint, headers)
	if err != nil {
		return nil, newExecuteRolloutHandshakeError(err, response)
	}
	closeCode := websocket.CloseGoingAway
	closeReason := "RLE CLI ended the rollout connection."
	defer func() {
		closeExecuteRolloutWebSocket(connection, closeCode, closeReason)
	}()

	// A 101 alone does not prove RLE answered: the upgrade path is shared with the OpenEnv
	// instance template, and a server that ignores the offered subprotocol still completes
	// the handshake. Require the negotiated value before writing, so a wrong handler is a
	// handshake failure rather than a rollout submitted into the void. Nothing has been
	// sent yet, so this stays safe to retry on HTTP.
	if negotiated := connection.Subprotocol(); negotiated != executeRolloutSubprotocol {
		return nil, newExecuteRolloutHandshakeError(fmt.Errorf(
			"server did not select the %q subprotocol (got %q)",
			executeRolloutSubprotocol, negotiated,
		), nil)
	}

	connection.SetReadLimit(maxExecuteRolloutFrameBytes)

	// gorilla honors the context during the handshake only. Tripping the read deadline is
	// what unblocks the read below when the caller cancels.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.SetReadDeadline(time.Now())
		case <-finished:
		}
	}()

	frame, err := encodeExecuteRolloutFrame(
		executeRolloutFrameHeader{Type: executeRolloutFrameExecute, RolloutID: request.RolloutID},
		request,
	)
	if err != nil {
		return nil, err
	}
	if err := connection.SetWriteDeadline(time.Now().Add(executeRolloutWriteTimeout)); err != nil {
		return nil, fmt.Errorf("send Execute Rollout request: %w", err)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return nil, fmt.Errorf("send Execute Rollout request: %w", err)
	}

	// Stay in the read: a connection with no read in progress does not answer the server's
	// keepalive pings, and a rollout can run for minutes between frames.
	for {
		messageType, message, err := connection.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("read Execute Rollout response: %w", err)
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		header, payload, err := decodeExecuteRolloutFrame(message)
		if err != nil {
			return nil, err
		}
		if header.RolloutID != request.RolloutID {
			// One rollout per connection here, so anything else is not ours.
			continue
		}
		switch header.Type {
		case executeRolloutFrameCompleted:
			var result executeRolloutResponse
			if err := json.Unmarshal(payload, &result); err != nil {
				return nil, fmt.Errorf("decode RLE response: %w", err)
			}
			closeCode = websocket.CloseNormalClosure
			closeReason = "RLE rollout complete."
			return &result, nil
		case executeRolloutFrameError:
			closeCode = websocket.CloseNormalClosure
			closeReason = "RLE rollout completed with an error."
			return nil, newExecuteRolloutFrameError(payload)
		default:
			continue
		}
	}
}

// closeExecuteRolloutWebSocket completes the WebSocket close handshake when possible.
// Conn.Close alone closes the network connection without sending a close frame, which
// causes the peer to observe an abnormal closure even after a completed rollout.
func closeExecuteRolloutWebSocket(connection *websocket.Conn, code int, reason string) {
	deadline := time.Now().Add(executeRolloutCloseTimeout)
	if err := connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		deadline,
	); err == nil {
		_ = connection.SetReadDeadline(deadline)
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				break
			}
		}
	}
	_ = connection.Close()
}

// newExecuteRolloutFrameError converts an error frame into the same error type the HTTP
// path produces, so callers keep classifying failures one way. After the upgrade the service
// reports failures as application messages and never as a changed HTTP status, so a status is
// synthesized: codes naming a missing resource map to 404 to preserve isRleNotFound, and
// everything else is reported as a rejected request.
func newExecuteRolloutFrameError(payload []byte) error {
	statusCode := http.StatusBadRequest
	var details rleErrorBody
	if err := json.Unmarshal(payload, &details); err == nil {
		if strings.HasSuffix(details.primary().Code, "NotFound") {
			statusCode = http.StatusNotFound
		}
	}
	return newRleHTTPError(statusCode, payload)
}

// encodeExecuteRolloutFrame builds one application message:
//
//	[4-byte unsigned big-endian header length][UTF-8 JSON header][UTF-8 JSON payload]
//
// The length counts header bytes only. The message boundary terminates the payload, so the
// payload carries no length of its own.
func encodeExecuteRolloutFrame(header executeRolloutFrameHeader, payload any) ([]byte, error) {
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshal Execute Rollout frame header: %w", err)
	}
	if len(headerBytes) > maxExecuteRolloutHeaderBytes {
		return nil, errors.New("Execute Rollout frame header is too large")
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Execute Rollout frame payload: %w", err)
	}
	frame := make([]byte, 4, 4+len(headerBytes)+len(payloadBytes))
	// Bounded by the maxExecuteRolloutHeaderBytes check above, so this cannot overflow.
	binary.BigEndian.PutUint32(frame[:4], uint32(len(headerBytes))) //nolint:gosec // bounded above
	frame = append(frame, headerBytes...)
	frame = append(frame, payloadBytes...)
	return frame, nil
}

func decodeExecuteRolloutFrame(frame []byte) (executeRolloutFrameHeader, []byte, error) {
	var header executeRolloutFrameHeader
	if len(frame) < 4 {
		return header, nil, errors.New("Execute Rollout frame is shorter than its header length prefix")
	}
	headerLength := binary.BigEndian.Uint32(frame[:4])
	if headerLength > maxExecuteRolloutHeaderBytes {
		return header, nil, errors.New("Execute Rollout frame header exceeds the supported size")
	}
	// The length check above guarantees len(frame)-4 is not negative.
	if uint64(headerLength) > uint64(len(frame)-4) { //nolint:gosec // non-negative by the check above
		return header, nil, errors.New("Execute Rollout frame header length exceeds the frame")
	}
	if err := json.Unmarshal(frame[4:4+headerLength], &header); err != nil {
		return header, nil, fmt.Errorf("decode Execute Rollout frame header: %w", err)
	}
	return header, frame[4+headerLength:], nil
}
