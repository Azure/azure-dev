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

// Execute Rollout v2 adds connection-level deployment-drain notifications. V1 remains
// available for services that have not deployed the rotation protocol yet.
const (
	executeRolloutSubprotocolV2 = "rle.execute-rollout.v2"
	executeRolloutSubprotocolV1 = "rle.execute-rollout.v1"

	// Retained for tests and callers that specifically exercise the original protocol.
	executeRolloutSubprotocol = executeRolloutSubprotocolV1
)

var executeRolloutSubprotocols = []string{
	executeRolloutSubprotocolV2,
	executeRolloutSubprotocolV1,
}

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
	executeRolloutFrameProgress  = "progress"
	executeRolloutFrameCompleted = "completed"
	executeRolloutFrameError     = "error"
	executeRolloutFrameDraining  = "server-draining"
)

// executeRolloutFrameHeader is the JSON header of an application message. Rollout messages
// own rollout_id; v2 connection-level messages, such as server-draining, intentionally omit it.
type executeRolloutFrameHeader struct {
	Type      string `json:"type"`
	RolloutID string `json:"rollout_id,omitempty"`
}

type executeRolloutDrainNotification struct {
	DrainTimeoutSeconds int `json:"drain_timeout_seconds"`
	RetryAfterSeconds   int `json:"retry_after_seconds"`
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
		Subprotocols:     executeRolloutSubprotocols,
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
	requestID  string
}

func (e *executeRolloutHandshakeError) Error() string {
	if e.statusCode != 0 {
		return withRequestID(
			fmt.Sprintf("Execute Rollout WebSocket upgrade failed with HTTP %d: %s", e.statusCode, e.body), e.requestID)
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
	result.requestID = serviceRequestID(response.Header, []byte(result.body))
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

// executeRolloutOverWebSocket uses the connection manager even for the command's single
// rollout. Keeping the single-rollout API here preserves the HTTP fallback boundary while
// the manager can retain draining sockets and rotate new submissions to replacements.
func (c *rleClient) executeRolloutOverWebSocket(
	ctx context.Context,
	environmentName string,
	environmentVersion string,
	loomBearerToken string,
	request executeRolloutRequest,
	onProgress func(executeRolloutProgress) error,
) (*executeRolloutResponse, error) {
	manager := newExecuteRolloutConnectionManager(
		ctx,
		c,
		environmentName,
		environmentVersion,
		loomBearerToken,
	)
	defer manager.Close()
	return manager.Execute(ctx, request, onProgress)
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
	if strings.TrimSpace(header.Type) == "" {
		return header, nil, errors.New("Execute Rollout frame header is missing type")
	}
	if header.Type != executeRolloutFrameDraining && strings.TrimSpace(header.RolloutID) == "" {
		return header, nil, fmt.Errorf("Execute Rollout %s frame header is missing rollout_id", header.Type)
	}
	return header, frame[4+headerLength:], nil
}
