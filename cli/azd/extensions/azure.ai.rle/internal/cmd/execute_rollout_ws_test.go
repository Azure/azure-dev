// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestExecuteRolloutWebSocketURLUsesTheGatewayForwardedAlias(t *testing.T) {
	client := newRleClientWithCredential("https://rle.test"+testFoundryProjectPath, &testTokenCredential{})

	endpoint, err := client.executeRolloutWebSocketURL("code_rl", "1.0.0", "abc123")
	if err != nil {
		t.Fatal(err)
	}

	expected := "wss://rle.test" + testFoundryProjectPath +
		"/rl_environments/code_rl/versions/1.0.0" +
		"/instance_groups/_execute_rollout/instances/abc123/openenv/ws" +
		"?api-version=" + foundryAPIVersion
	if endpoint != expected {
		t.Fatalf("expected %s, got %s", expected, endpoint)
	}
}

// The gateway only forwards the upgrade on the OpenEnv instance template, so the rollout
// path this replaces must not reappear in the URL.
func TestExecuteRolloutWebSocketURLDoesNotUseTheUnregisteredRolloutPath(t *testing.T) {
	client := newRleClientWithCredential("https://rle.test"+testFoundryProjectPath, &testTokenCredential{})

	endpoint, err := client.executeRolloutWebSocketURL("code_rl", "1.0.0", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(endpoint, "/rollouts/ws") {
		t.Fatalf("URL still uses the path the gateway rejects: %s", endpoint)
	}
}

func TestExecuteRolloutFrameRoundTrips(t *testing.T) {
	header := executeRolloutFrameHeader{Type: executeRolloutFrameExecute, RolloutID: "r123"}

	frame, err := encodeExecuteRolloutFrame(header, map[string]string{"task": "example"})
	if err != nil {
		t.Fatal(err)
	}

	// The prefix counts header bytes only, not the payload.
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	if prefix := binary.BigEndian.Uint32(frame[:4]); prefix != uint32(len(headerBytes)) { //nolint:gosec // test data
		t.Fatalf("expected prefix %d, got %d", len(headerBytes), prefix)
	}

	decoded, payload, err := decodeExecuteRolloutFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != header {
		t.Fatalf("expected %#v, got %#v", header, decoded)
	}
	if string(payload) != `{"task":"example"}` {
		t.Fatalf("unexpected payload %s", payload)
	}
}

func TestDecodeExecuteRolloutFrameRejectsATruncatedFrame(t *testing.T) {
	frame := make([]byte, 4)
	binary.BigEndian.PutUint32(frame, 32)

	if _, _, err := decodeExecuteRolloutFrame(append(frame, []byte(`{"type":`)...)); err == nil {
		t.Fatal("expected a header length exceeding the frame to be rejected")
	}
	if _, _, err := decodeExecuteRolloutFrame([]byte{0, 0}); err == nil {
		t.Fatal("expected a frame shorter than the prefix to be rejected")
	}
}

// stubExecuteRolloutWebSocketServer serves one rollout and replies with the frame the
// handler returns, so the test exercises real framing over a real socket.
func stubExecuteRolloutWebSocketServer(
	t *testing.T,
	reply func(header executeRolloutFrameHeader, payload []byte) (executeRolloutFrameHeader, any),
) *httptest.Server {
	t.Helper()
	return stubExecuteRolloutWebSocketStreamServer(
		t,
		func(header executeRolloutFrameHeader, payload []byte) []executeRolloutTestReply {
			replyHeader, replyPayload := reply(header, payload)
			return []executeRolloutTestReply{{header: replyHeader, payload: replyPayload}}
		},
	)
}

type executeRolloutTestReply struct {
	header  executeRolloutFrameHeader
	payload any
}

func stubExecuteRolloutWebSocketStreamServer(
	t *testing.T,
	reply func(header executeRolloutFrameHeader, payload []byte) []executeRolloutTestReply,
) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{Subprotocols: []string{executeRolloutSubprotocol}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Sec-WebSocket-Protocol"); !strings.Contains(got, executeRolloutSubprotocol) {
			t.Errorf("expected the client to offer %s, got %q", executeRolloutSubprotocol, got)
		}
		if r.Header.Get(executeRolloutHeader) != "loom-token" {
			t.Errorf("expected the forwarded Loom token, got %q", r.Header.Get(executeRolloutHeader))
		}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		_, message, err := connection.ReadMessage()
		if err != nil {
			t.Errorf("read: %v", err)
			return
		}
		header, payload, err := decodeExecuteRolloutFrame(message)
		if err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		for _, response := range reply(header, payload) {
			frame, err := encodeExecuteRolloutFrame(response.header, response.payload)
			if err != nil {
				t.Errorf("encode: %v", err)
				return
			}
			if err := connection.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}
		if _, _, err := connection.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			t.Errorf("expected a normal client close after the rollout response, got %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// stubExecuteRolloutDialer points the dial seam at a local server while asserting the CLI
// asked for the alias URL. gorilla's Dialer does not run through httpClient.Transport.
func stubExecuteRolloutDialer(t *testing.T, serverURL string, observed *string) {
	t.Helper()
	previous := dialExecuteRolloutWebSocket
	dialExecuteRolloutWebSocket = func(
		ctx context.Context,
		endpoint string,
		headers http.Header,
	) (*websocket.Conn, *http.Response, error) {
		if observed != nil {
			*observed = endpoint
		}
		dialer := &websocket.Dialer{Subprotocols: []string{executeRolloutSubprotocol}}
		return dialer.DialContext(ctx, strings.Replace(serverURL, "http://", "ws://", 1), headers)
	}
	t.Cleanup(func() { dialExecuteRolloutWebSocket = previous })
}

func TestExecuteRolloutOverWebSocketReturnsTheCompletedFrame(t *testing.T) {
	server := stubExecuteRolloutWebSocketServer(
		t,
		func(header executeRolloutFrameHeader, payload []byte) (executeRolloutFrameHeader, any) {
			if header.Type != executeRolloutFrameExecute {
				t.Errorf("expected an execute frame, got %q", header.Type)
			}
			var request executeRolloutRequest
			if err := json.Unmarshal(payload, &request); err != nil {
				t.Errorf("payload is not an Execute Rollout request: %v", err)
			}
			if request.RolloutID != header.RolloutID {
				t.Errorf("payload rollout ID %q disagrees with header %q", request.RolloutID, header.RolloutID)
			}
			return executeRolloutFrameHeader{
					Type:      executeRolloutFrameCompleted,
					RolloutID: header.RolloutID,
				}, map[string]any{
					"rollout_id": header.RolloutID,
					"reward":     1.0,
					"success":    true,
				}
		},
	)
	var observed string
	stubExecuteRolloutDialer(t, server.URL, &observed)

	client := newRleClientWithCredential("https://rle.test"+testFoundryProjectPath, &testTokenCredential{})
	response, err := client.executeRollout(
		t.Context(), "code_rl", "1.0.0", "loom-token",
		executeRolloutRequest{RolloutID: "abc123", Task: json.RawMessage(`{"prompt":"hi"}`)},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.RolloutID != "abc123" || response.Success == nil || !*response.Success {
		t.Fatalf("unexpected response %#v", response)
	}
	if !strings.Contains(observed, "/instance_groups/_execute_rollout/instances/abc123/openenv/ws") {
		t.Fatalf("expected the alias URL, got %s", observed)
	}
}

func TestExecuteRolloutOverWebSocketMapsAnErrorFrameToAnRleError(t *testing.T) {
	server := stubExecuteRolloutWebSocketServer(
		t,
		func(header executeRolloutFrameHeader, _ []byte) (executeRolloutFrameHeader, any) {
			return executeRolloutFrameHeader{
					Type:      executeRolloutFrameError,
					RolloutID: header.RolloutID,
				}, map[string]string{
					"code":    "EnvironmentNotFound",
					"message": "The environment version was not found.",
				}
		},
	)
	stubExecuteRolloutDialer(t, server.URL, nil)

	client := newRleClientWithCredential("https://rle.test"+testFoundryProjectPath, &testTokenCredential{})
	_, err := client.executeRollout(
		t.Context(), "code_rl", "1.0.0", "loom-token",
		executeRolloutRequest{RolloutID: "abc123"},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("expected the error frame to surface as an error")
	}
	// The rollout command distinguishes a missing environment version this way, so an
	// error frame has to classify the same as the HTTP status it replaces.
	if !isRleNotFound(err) {
		t.Fatalf("expected a not-found classification, got %v", err)
	}
	if !strings.Contains(err.Error(), "The environment version was not found.") {
		t.Fatalf("expected the service message to survive, got %v", err)
	}
}

// Where the alias is not published the upgrade is refused before any rollout is requested,
// so the HTTP transport must still run and the result must be unchanged.
func TestExecuteRolloutFallsBackToHTTPWhenTheUpgradeIsRejected(t *testing.T) {
	var httpCalls int
	rleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalls++
		if !strings.HasSuffix(r.URL.Path, ":executeRollout") {
			t.Errorf("expected the HTTP execute rollout action, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rollout_id":"abc123","reward":1,"success":true}`))
	}))
	defer rleServer.Close()

	// The gateway's rejection of the unregistered path, which is what a client sees where
	// the alias has not been deployed.
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(
			`{"error":{"code":"API endpoint does not match resource",` +
				`"message":"API called azure-ai-projects-websocket-api not matching any APIs in resource."}}`))
	}))
	defer gateway.Close()
	stubExecuteRolloutDialer(t, gateway.URL, nil)

	client := testRleClientForServer(t, rleServer.URL)
	var errOut bytes.Buffer
	response, err := client.executeRollout(
		t.Context(), "code_rl", "1.0.0", "loom-token",
		executeRolloutRequest{RolloutID: "abc123"},
		nil,
		&errOut,
	)
	if err != nil {
		t.Fatal(err)
	}
	if httpCalls != 1 {
		t.Fatalf("expected exactly one HTTP fallback call, got %d", httpCalls)
	}
	if response.RolloutID != "abc123" {
		t.Fatalf("unexpected response %#v", response)
	}
	if !strings.Contains(errOut.String(), "Falling back to the HTTP transport") {
		t.Fatalf("expected the fallback to be reported, got %q", errOut.String())
	}
}

// A failure after the upgrade must not be retried on HTTP: the rollout may already have run.
func TestExecuteRolloutDoesNotFallBackAfterTheUpgradeSucceeds(t *testing.T) {
	var httpCalls int
	rleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rollout_id":"abc123","reward":1,"success":true}`))
	}))
	defer rleServer.Close()

	server := stubExecuteRolloutWebSocketServer(
		t,
		func(header executeRolloutFrameHeader, _ []byte) (executeRolloutFrameHeader, any) {
			return executeRolloutFrameHeader{
				Type:      executeRolloutFrameError,
				RolloutID: header.RolloutID,
			}, map[string]string{"code": "InternalServerError", "message": "boom"}
		},
	)
	stubExecuteRolloutDialer(t, server.URL, nil)

	client := testRleClientForServer(t, rleServer.URL)
	if _, err := client.executeRollout(
		t.Context(), "code_rl", "1.0.0", "loom-token",
		executeRolloutRequest{RolloutID: "abc123"},
		nil,
		nil,
	); err == nil {
		t.Fatal("expected the error frame to fail the rollout")
	}
	if httpCalls != 0 {
		t.Fatalf("expected no HTTP retry after a successful upgrade, got %d calls", httpCalls)
	}
}

// gorilla completes the handshake even when the server ignores the offered subprotocol, so
// a 101 from a handler that is not RLE would otherwise look like a healthy rollout socket.
// Nothing has been written at that point, so falling back is still safe.
func TestExecuteRolloutFallsBackWhenTheServerDoesNotSelectTheSubprotocol(t *testing.T) {
	var httpCalls int
	rleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rollout_id":"abc123","reward":1,"success":true}`))
	}))
	defer rleServer.Close()

	framed := make(chan bool, 1)
	upgrader := websocket.Upgrader{} // Configured with no Subprotocols, so it selects none.
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			framed <- false
			return
		}
		defer connection.Close()
		_, _, err = connection.ReadMessage()
		framed <- err == nil
	}))
	defer gateway.Close()
	stubExecuteRolloutDialer(t, gateway.URL, nil)

	client := testRleClientForServer(t, rleServer.URL)
	var errOut bytes.Buffer
	if _, err := client.executeRollout(
		t.Context(), "code_rl", "1.0.0", "loom-token",
		executeRolloutRequest{RolloutID: "abc123"},
		nil,
		&errOut,
	); err != nil {
		t.Fatal(err)
	}
	if <-framed {
		t.Fatal("expected no execute frame on a socket that skipped the subprotocol")
	}
	if httpCalls != 1 {
		t.Fatalf("expected exactly one HTTP fallback call, got %d", httpCalls)
	}
	if !strings.Contains(errOut.String(), "Falling back to the HTTP transport") {
		t.Fatalf("expected the fallback to be reported, got %q", errOut.String())
	}
}

func TestExecuteRolloutOverWebSocketStreamsProgressBeforeCompletion(t *testing.T) {
	server := stubExecuteRolloutWebSocketStreamServer(
		t,
		func(header executeRolloutFrameHeader, _ []byte) []executeRolloutTestReply {
			return []executeRolloutTestReply{
				{
					header: executeRolloutFrameHeader{
						Type:      executeRolloutFrameProgress,
						RolloutID: header.RolloutID,
					},
					payload: map[string]any{
						"message":    "Waiting for a model response for step 1/5.",
						"phase":      "model_call",
						"status":     "started",
						"sequence":   1,
						"elapsed_ms": 1250,
					},
				},
				{
					header: executeRolloutFrameHeader{
						Type:      executeRolloutFrameProgress,
						RolloutID: header.RolloutID,
					},
					payload: map[string]any{
						"message":    "Step 1/5 finished; reward 0.25, total reward 0.25.",
						"phase":      "environment_step",
						"status":     "completed",
						"sequence":   3,
						"elapsed_ms": 4200,
						"partial_response": map[string]any{
							"rollout_id": header.RolloutID,
							"reward":     0.25,
							"episode": map[string]any{
								"kind": "gym_openenv",
								"steps": []map[string]any{{
									"capture_node_id": "node-1",
									"reward":          0.25,
									"episode_done":    false,
								}},
							},
						},
					},
				},
				{
					header: executeRolloutFrameHeader{
						Type:      executeRolloutFrameCompleted,
						RolloutID: header.RolloutID,
					},
					payload: map[string]any{
						"rollout_id": header.RolloutID,
						"reward":     1.0,
						"success":    true,
					},
				},
			}
		},
	)
	stubExecuteRolloutDialer(t, server.URL, nil)

	client := newRleClientWithCredential("https://rle.test"+testFoundryProjectPath, &testTokenCredential{})
	var progress []executeRolloutProgress
	response, err := client.executeRollout(
		t.Context(), "code_rl", "1.0.0", "loom-token",
		executeRolloutRequest{RolloutID: "abc123"},
		func(update executeRolloutProgress) error {
			progress = append(progress, update)
			return nil
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Reward != 1 || response.Success == nil || !*response.Success {
		t.Fatalf("unexpected response %#v", response)
	}
	if len(progress) != 2 {
		t.Fatalf("expected 2 progress updates, got %#v", progress)
	}
	if progress[0].Sequence != 1 || progress[1].Sequence != 3 {
		t.Fatalf("sequence gaps should be accepted, got %#v", progress)
	}
	if progress[1].PartialResponse == nil ||
		progress[1].PartialResponse.Reward == nil ||
		*progress[1].PartialResponse.Reward != 0.25 ||
		progress[1].PartialResponse.Episode == nil ||
		len(progress[1].PartialResponse.Episode.Steps) != 1 {
		t.Fatalf("unexpected partial response %#v", progress[1].PartialResponse)
	}
}
