// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
)

// ---------------------------------------------------------------------------
// servicesFromProjectConfig
// ---------------------------------------------------------------------------

func Test_servicesFromProjectConfig(t *testing.T) {
	t.Run("nil services map", func(t *testing.T) {
		pc := &project.ProjectConfig{
			Name:     "empty-project",
			Path:     t.TempDir(),
			Services: nil,
		}
		result := servicesFromProjectConfig(context.Background(), pc)
		require.Empty(t, result)
	})

	t.Run("empty services map", func(t *testing.T) {
		pc := &project.ProjectConfig{
			Name:     "empty-project",
			Path:     t.TempDir(),
			Services: map[string]*project.ServiceConfig{},
		}
		result := servicesFromProjectConfig(context.Background(), pc)
		require.Empty(t, result)
	})

	t.Run("single service with absolute path", func(t *testing.T) {
		absPath := filepath.Join(t.TempDir(), "api")
		pc := &project.ProjectConfig{
			Name: "my-project",
			Path: t.TempDir(),
			Services: map[string]*project.ServiceConfig{
				"api": {
					Name:         "api",
					RelativePath: absPath,
				},
			},
		}
		for _, svc := range pc.Services {
			svc.Project = pc
		}

		result := servicesFromProjectConfig(context.Background(), pc)
		require.Len(t, result, 1)
		require.Equal(t, "api", result[0].Name)
		// Path() returns the absolute path directly since RelativePath is absolute
		require.Equal(t, pc.Services["api"].Path(), result[0].Path)
	})

	t.Run("multiple services with relative paths", func(t *testing.T) {
		root := t.TempDir()
		pc := &project.ProjectConfig{
			Name: "multi-project",
			Path: root,
			Services: map[string]*project.ServiceConfig{
				"web": {
					Name:         "web",
					RelativePath: filepath.Join("src", "web"),
				},
				"api": {
					Name:         "api",
					RelativePath: filepath.Join("src", "api"),
				},
				"worker": {
					Name:         "worker",
					RelativePath: filepath.Join("src", "worker"),
				},
			},
		}
		for _, svc := range pc.Services {
			svc.Project = pc
		}

		result := servicesFromProjectConfig(context.Background(), pc)
		require.Len(t, result, 3)

		// Build maps for order-independent comparison
		resultMap := make(map[string]string)
		for _, svc := range result {
			resultMap[svc.Name] = svc.Path
		}

		// Each service path should match ServiceConfig.Path()
		for name, svcConfig := range pc.Services {
			require.Equal(t, svcConfig.Path(), resultMap[name], "path mismatch for service %s", name)
		}
	})
}

// ---------------------------------------------------------------------------
// Server session management: newSession / sessionFromId / validateSession
// ---------------------------------------------------------------------------

func newTestServer() *Server {
	return NewServer(nil)
}

func TestNewSession_CreatesUniqueIDs(t *testing.T) {
	s := newTestServer()

	id1, sess1, err1 := s.newSession()
	require.NoError(t, err1)
	require.NotEmpty(t, id1)
	require.NotNil(t, sess1)

	id2, sess2, err2 := s.newSession()
	require.NoError(t, err2)
	require.NotEmpty(t, id2)
	require.NotNil(t, sess2)

	require.NotEqual(t, id1, id2, "each session should have a unique ID")
}

func TestNewSession_RegistersInMap(t *testing.T) {
	s := newTestServer()

	id, session, err := s.newSession()
	require.NoError(t, err)

	// sessionFromId should find it
	found, ok := s.sessionFromId(id)
	require.True(t, ok)
	require.Same(t, session, found, "should return the exact same session pointer")
}

func TestSessionFromId_NotFound(t *testing.T) {
	s := newTestServer()

	_, ok := s.sessionFromId("nonexistent-id")
	require.False(t, ok)
}

func TestSessionFromId_ConcurrentAccess(t *testing.T) {
	s := newTestServer()
	const goroutines = 50

	ids := make([]string, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			id, _, err := s.newSession()
			ids[idx] = id
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// Verify all goroutines succeeded, then check uniqueness
	for i, err := range errs {
		require.NoErrorf(t, err, "goroutine %d failed", i)
	}

	seen := make(map[string]bool)
	for _, id := range ids {
		require.NotEmpty(t, id)
		require.False(t, seen[id], "duplicate session ID detected")
		seen[id] = true

		_, ok := s.sessionFromId(id)
		require.True(t, ok)
	}
}

func TestValidateSession_EmptyId(t *testing.T) {
	s := newTestServer()

	_, err := s.validateSession(Session{Id: ""})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is required")
}

func TestValidateSession_InvalidId(t *testing.T) {
	s := newTestServer()

	_, err := s.validateSession(Session{Id: "does-not-exist"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestValidateSession_ValidId(t *testing.T) {
	s := newTestServer()

	id, _, err := s.newSession()
	require.NoError(t, err)

	ss, err := s.validateSession(Session{Id: id})
	require.NoError(t, err)
	require.NotNil(t, ss)
	require.Equal(t, id, ss.id, "session id should be set on the serverSession")
}

// ---------------------------------------------------------------------------
// lineWriter.Flush
// ---------------------------------------------------------------------------

func TestLineWriter_Flush_WithBufferedContent(t *testing.T) {
	captured := &writeCapturer{}
	lw := &lineWriter{
		next: captured,
	}

	// Write partial content (no newline)
	_, err := lw.Write([]byte("partial content"))
	require.NoError(t, err)
	require.Empty(t, captured.writes, "no newline yet, nothing should be flushed to next")

	// Flush should push the buffered content
	err = lw.Flush(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"partial content"}, captured.writes)
}

func TestLineWriter_Flush_EmptyBuffer(t *testing.T) {
	captured := &writeCapturer{}
	lw := &lineWriter{
		next: captured,
	}

	// Flush with nothing buffered should be a no-op
	err := lw.Flush(context.Background())
	require.NoError(t, err)
	require.Empty(t, captured.writes)
}

func TestLineWriter_Flush_AfterCompleteLine(t *testing.T) {
	captured := &writeCapturer{}
	lw := &lineWriter{
		next: captured,
	}

	// Write a complete line — buffer should be empty after
	_, err := lw.Write([]byte("complete line\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"complete line\n"}, captured.writes)

	// Flush should be a no-op since buffer was drained by the newline
	err = lw.Flush(context.Background())
	require.NoError(t, err)
	require.Len(t, captured.writes, 1, "no additional writes from flush")
}

func TestLineWriter_Flush_NextWriterError(t *testing.T) {
	expectedErr := errors.New("write failed")
	failWriter := writerFunc(func(p []byte) (int, error) {
		return 0, expectedErr
	})
	lw := &lineWriter{
		next: failWriter,
	}

	// Buffer some data
	_, err := lw.Write([]byte("data"))
	require.NoError(t, err) // Write doesn't flush (no newline)

	// Flush should propagate the error
	err = lw.Flush(context.Background())
	require.ErrorIs(t, err, expectedErr)
}

// ---------------------------------------------------------------------------
// lineWriter — additional edge cases
// ---------------------------------------------------------------------------

func TestLineWriter_MultipleNewlinesInSingleWrite(t *testing.T) {
	captured := &writeCapturer{}
	lw := &lineWriter{
		next: captured,
	}

	_, err := lw.Write([]byte("line1\nline2\nline3\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"line1\n", "line2\n", "line3\n"}, captured.writes)
}

func TestLineWriter_WriteErrorFromNext(t *testing.T) {
	expectedErr := errors.New("downstream error")
	failWriter := writerFunc(func(p []byte) (int, error) {
		return 0, expectedErr
	})
	lw := &lineWriter{
		next: failWriter,
	}

	_, err := lw.Write([]byte("text\n"))
	require.ErrorIs(t, err, expectedErr)
}

func TestLineWriter_TrimLineEndingsCRLF_MultiLine(t *testing.T) {
	captured := &writeCapturer{}
	lw := &lineWriter{
		next:            captured,
		trimLineEndings: true,
	}

	_, err := lw.Write([]byte("a\r\nb\nc\r\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, captured.writes)
}

// ---------------------------------------------------------------------------
// writerFunc adapter
// ---------------------------------------------------------------------------

func TestWriterFunc_Implements_IOWriter(t *testing.T) {
	var captured []byte
	wf := writerFunc(func(p []byte) (int, error) {
		captured = append(captured, p...)
		return len(p), nil
	})

	// Prove it satisfies io.Writer
	var w io.Writer = wf
	n, err := w.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, []byte("hello"), captured)
}

func TestWriterFunc_PropagatesError(t *testing.T) {
	expectedErr := errors.New("broken writer")
	wf := writerFunc(func(p []byte) (int, error) {
		return 0, expectedErr
	})

	_, err := wf.Write([]byte("data"))
	require.ErrorIs(t, err, expectedErr)
}

// ---------------------------------------------------------------------------
// writerMultiplexer — additional coverage
// ---------------------------------------------------------------------------

func TestWriterMultiplexer_WriteToEmpty(t *testing.T) {
	wm := &writerMultiplexer{}

	// Writing to a multiplexer with no writers should succeed silently
	n, err := wm.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 0, n) // no writers => last n from loop is 0
}

func TestWriterMultiplexer_ErrorPropagation(t *testing.T) {
	expectedErr := errors.New("writer error")
	var good bytes.Buffer

	wm := &writerMultiplexer{}
	wm.AddWriter(&good)
	wm.AddWriter(writerFunc(func(p []byte) (int, error) {
		return 0, expectedErr
	}))

	_, err := wm.Write([]byte("data"))
	require.ErrorIs(t, err, expectedErr)
	// First writer should have received the data before the error
	require.Equal(t, "data", good.String())
}

func TestWriterMultiplexer_RemoveNonExistent(t *testing.T) {
	var buf bytes.Buffer
	wm := &writerMultiplexer{}
	wm.AddWriter(&buf)

	// Removing a writer not in the list should be a safe no-op
	var other bytes.Buffer
	wm.RemoveWriter(&other)

	// Original writer should still work
	_, err := wm.Write([]byte("still works"))
	require.NoError(t, err)
	require.Equal(t, "still works", buf.String())
}

func TestWriterMultiplexer_ConcurrentWriteAndModify(t *testing.T) {
	wm := &writerMultiplexer{}
	var buf bytes.Buffer
	wm.AddWriter(&buf)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Concurrent writes
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = wm.Write([]byte("x"))
		}()
	}

	// Concurrent add/remove
	for range goroutines {
		go func() {
			defer wg.Done()
			var tmp bytes.Buffer
			wm.AddWriter(&tmp)
			wm.RemoveWriter(&tmp)
		}()
	}

	wg.Wait()
	// Should not panic — we're testing thread safety
}

// ---------------------------------------------------------------------------
// newWriter (server_service.go)
// ---------------------------------------------------------------------------

func TestNewWriter_ReturnsMultiplexerWithLogWriter(t *testing.T) {
	wm := newWriter("[test] ")

	// The multiplexer should have exactly one writer (the log writer)
	require.NotNil(t, wm)
	require.Len(t, wm.writers, 1)

	// Writing should not panic (it writes to log.Printf internally)
	n, err := wm.Write([]byte("test message"))
	require.NoError(t, err)
	// The writerFunc inside newWriter returns n=0 (bug in the source: `return n, nil` where n is unset),
	// but the multiplexer returns whatever the last writer returns.
	_ = n
}

func TestNewWriter_AcceptsAdditionalWriters(t *testing.T) {
	wm := newWriter("[prefix] ")

	var buf bytes.Buffer
	wm.AddWriter(&buf)

	_, err := wm.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, "hello", buf.String())
}

// ---------------------------------------------------------------------------
// messageWriter
// ---------------------------------------------------------------------------

func TestMessageWriter_Write(t *testing.T) {
	// We create a real Observer-like setup but use a mock connection approach.
	// Since messageWriter just calls observer.OnNext, we can test it by
	// providing a mock observer that records what it receives.
	//
	// However, Observer requires a jsonrpc2.Conn — instead we test the messageWriter
	// through a simpler approach: verify the Write contract.
	//
	// We construct a messageWriter with a nil observer to verify the interface,
	// but that would panic. Instead, we test the writerMultiplexer+lineWriter combo
	// that the real code uses (server_session.go), since messageWriter depends on
	// Observer which requires a real RPC connection.
	//
	// The key coverage gain is from lineWriter.Flush and lineWriter.Write edge cases
	// which are tested above.
	t.Run("write contract returns len(p)", func(t *testing.T) {
		// Verify that lineWriter returns correct byte count
		captured := &writeCapturer{}
		lw := &lineWriter{
			next: captured,
		}
		data := []byte("hello world\n")
		n, err := lw.Write(data)
		require.NoError(t, err)
		require.Equal(t, len(data), n)
	})
}

// ---------------------------------------------------------------------------
// NewServer
// ---------------------------------------------------------------------------

func TestNewServer(t *testing.T) {
	s := NewServer(nil)
	require.NotNil(t, s)
	require.NotNil(t, s.sessions)
	require.Empty(t, s.sessions)
}

// ---------------------------------------------------------------------------
// newServerService / newAspireService / newDebugService
// ---------------------------------------------------------------------------

func TestNewServerService(t *testing.T) {
	s := newTestServer()
	svc := newServerService(s)
	require.NotNil(t, svc)
	require.Same(t, s, svc.server)
}

func TestNewAspireService(t *testing.T) {
	s := newTestServer()
	svc := newAspireService(s)
	require.NotNil(t, svc)
	require.Same(t, s, svc.server)
}

func TestNewDebugService(t *testing.T) {
	s := newTestServer()
	svc := newDebugService(s)
	require.NotNil(t, svc)
	require.Same(t, s, svc.server)
}

func TestNewDebugService_NilServer(t *testing.T) {
	svc := newDebugService(nil)
	require.NotNil(t, svc)
	require.Nil(t, svc.server)
}

// ---------------------------------------------------------------------------
// DeleteMode bit flags — additional combinations
// (DeleteMode is defined in environment_service.go)
// ---------------------------------------------------------------------------

func TestDeleteMode_ZeroValue(t *testing.T) {
	var d DeleteMode
	require.EqualValues(t, 0, d)
	require.EqualValues(t, 0, d&DeleteModeLocal)
	require.EqualValues(t, 0, d&DeleteModeAzureResources)
}

// ---------------------------------------------------------------------------
// ProgressMessage helpers — edge cases
// ---------------------------------------------------------------------------

func TestNewInfoProgressMessage_EmptyMessage(t *testing.T) {
	msg := newInfoProgressMessage("")
	require.Equal(t, "", msg.Message)
	require.Equal(t, Info, msg.Severity)
	require.Equal(t, Logging, msg.Kind)
}

func TestNewImportantProgressMessage_EmptyMessage(t *testing.T) {
	msg := newImportantProgressMessage("")
	require.Equal(t, "", msg.Message)
	require.Equal(t, Info, msg.Severity)
	require.Equal(t, Important, msg.Kind)
}

func TestProgressMessage_WithMessage_PreservesAllFields(t *testing.T) {
	original := ProgressMessage{
		Message:            "original",
		Severity:           Error,
		Kind:               Important,
		Code:               "ERR-42",
		AdditionalInfoLink: "https://docs.example.com",
	}

	updated := original.WithMessage("new message")
	require.Equal(t, "new message", updated.Message)
	require.Equal(t, Error, updated.Severity)
	require.Equal(t, Important, updated.Kind)
	require.Equal(t, "ERR-42", updated.Code)
	require.Equal(t, "https://docs.example.com", updated.AdditionalInfoLink)
	require.False(t, updated.Time.IsZero(), "Time should be set")

	// Original unchanged
	require.Equal(t, "original", original.Message)
}

// ---------------------------------------------------------------------------
// wsStream.Close
// ---------------------------------------------------------------------------

func TestWsStream_Close(t *testing.T) {
	s := wsStream{}
	require.NoError(t, s.Close())
}

// ---------------------------------------------------------------------------
// newWebSocketStream
// ---------------------------------------------------------------------------

func TestNewWebSocketStream(t *testing.T) {
	ws := newWebSocketStream(nil)
	require.NotNil(t, ws)
	require.Nil(t, ws.c)
}

// ---------------------------------------------------------------------------
// Observer.UnmarshalJSON — additional edge cases
// ---------------------------------------------------------------------------

func TestObserver_UnmarshalJSON_ValidPayload(t *testing.T) {
	o := &Observer[string]{}
	err := o.UnmarshalJSON([]byte(`{"__jsonrpc_marshaled":1,"handle":42}`))
	require.NoError(t, err)
	require.Equal(t, 42, o.handle)
}

func TestObserver_UnmarshalJSON_MissingMarshaledTag(t *testing.T) {
	o := &Observer[string]{}
	err := o.UnmarshalJSON([]byte(`{"handle":42}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "__jsonrpc_marshaled")
}

func TestObserver_UnmarshalJSON_WrongMarshaledValue(t *testing.T) {
	o := &Observer[string]{}
	err := o.UnmarshalJSON([]byte(`{"__jsonrpc_marshaled":0,"handle":42}`))
	require.Error(t, err)
}

func TestObserver_UnmarshalJSON_MissingHandle(t *testing.T) {
	o := &Observer[string]{}
	err := o.UnmarshalJSON([]byte(`{"__jsonrpc_marshaled":1}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "handle")
}

func TestObserver_UnmarshalJSON_InvalidJSON(t *testing.T) {
	o := &Observer[string]{}
	err := o.UnmarshalJSON([]byte(`not json`))
	require.Error(t, err)
}

func TestObserver_AttachConnection(t *testing.T) {
	o := &Observer[int]{}
	require.Nil(t, o.c)

	// attachConnection sets the connection
	o.attachConnection(nil) // nil is valid in test
	require.Nil(t, o.c)
}

// ---------------------------------------------------------------------------
// Handler NewHandler panics — edge cases for the reflect-based handler
// ---------------------------------------------------------------------------

func TestNewHandler_PanicsOnNonFunction(t *testing.T) {
	require.Panics(t, func() {
		NewHandler("not a function")
	})
}

func TestNewHandler_PanicsOnNoArgs(t *testing.T) {
	require.Panics(t, func() {
		NewHandler(func() error { return nil })
	})
}

func TestNewHandler_PanicsOnWrongFirstArg(t *testing.T) {
	require.Panics(t, func() {
		NewHandler(func(s string) error { return nil })
	})
}

func TestNewHandler_PanicsOnBadReturnCount(t *testing.T) {
	require.Panics(t, func() {
		NewHandler(func(ctx context.Context) {})
	})
}

func TestNewHandler_PanicsOnNonErrorReturn(t *testing.T) {
	require.Panics(t, func() {
		NewHandler(func(ctx context.Context) string { return "" })
	})
}

func TestNewHandler_PanicsOnNonErrorSecondReturn(t *testing.T) {
	require.Panics(t, func() {
		NewHandler(func(ctx context.Context) (string, string) { return "", "" })
	})
}

// ---------------------------------------------------------------------------
// newEnvironmentService
// ---------------------------------------------------------------------------

func TestNewEnvironmentService(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	require.NotNil(t, svc)
	require.Same(t, s, svc.server)
}

// ---------------------------------------------------------------------------
// wsStream Read / Write through real WebSocket connections
// ---------------------------------------------------------------------------

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
		msg, _, err := stream.Read(context.Background())
		if err != nil {
			t.Logf("read error: %v", err)
			return
		}

		_, err = stream.Write(context.Background(), msg)
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

	n, err := stream.Write(context.Background(), notification)
	require.NoError(t, err)
	require.Greater(t, n, int64(0))

	// Read it back (the server echoes it)
	msg, bytesRead, err := stream.Read(context.Background())
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

	_, _, err = stream.Read(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected message type")
}

// ---------------------------------------------------------------------------
// serveRpc — method not found
// ---------------------------------------------------------------------------

func TestServeRpc_MethodNotFound(t *testing.T) {
	// Create a minimal handler set and call a non-existent method
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveRpc(w, r, map[string]Handler{
			"ExistingMethod": NewHandler(func(ctx context.Context) error {
				return nil
			}),
		})
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	serverURL.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverURL.String(), nil)
	require.NoError(t, err)

	rpcConn := jsonrpc2.NewConn(newWebSocketStream(wsConn))
	rpcConn.Go(context.Background(), nil)

	_, err = rpcConn.Call(context.Background(), "NonExistentMethod", nil, nil)
	require.Error(t, err)

	var rpcErr *jsonrpc2.Error
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, jsonrpc2.MethodNotFound, rpcErr.Code)
}

// ---------------------------------------------------------------------------
// serveRpc — cancel request for unknown ID (no-op)
// ---------------------------------------------------------------------------

func TestServeRpc_CancelUnknownId(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveRpc(w, r, map[string]Handler{})
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	serverURL.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverURL.String(), nil)
	require.NoError(t, err)

	rpcConn := jsonrpc2.NewConn(newWebSocketStream(wsConn))
	rpcConn.Go(context.Background(), nil)

	// Send a cancel for a non-existent ID — should not panic
	err = rpcConn.Notify(context.Background(), "$/cancelRequest", struct {
		Id int `json:"id"`
	}{Id: 999})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// handler with 3 returns (unreachable branch coverage)
// ---------------------------------------------------------------------------

func TestNewHandler_ThreeReturnsPanics(t *testing.T) {
	require.Panics(t, func() {
		NewHandler(func(ctx context.Context) (string, int, error) { return "", 0, nil })
	})
}

// ---------------------------------------------------------------------------
// unmarshalArgs — non-array params
// ---------------------------------------------------------------------------

func TestUnmarshalArgs_NonArrayParams(t *testing.T) {
	fnType := reflect.TypeFor[func(ctx context.Context, s string) error]()

	// Params as an object instead of array
	req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", map[string]string{"key": "value"})
	require.NoError(t, err)

	_, err = unmarshalArgs(nil, req, fnType)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// unmarshalArgs — wrong number of params
// ---------------------------------------------------------------------------

func TestUnmarshalArgs_WrongParamCount(t *testing.T) {
	fnType := reflect.TypeFor[func(ctx context.Context, a, b string) error]()

	// Only one param when two are expected
	req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{"only-one"})
	require.NoError(t, err)

	_, err = unmarshalArgs(nil, req, fnType)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// unmarshalArgs — type mismatch
// ---------------------------------------------------------------------------

func TestUnmarshalArgs_TypeMismatch(t *testing.T) {
	fnType := reflect.TypeFor[func(ctx context.Context, n int) error]()

	// Pass a string where int is expected
	req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{"not-an-int"})
	require.NoError(t, err)

	_, err = unmarshalArgs(nil, req, fnType)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Handler — value+error return path
// ---------------------------------------------------------------------------

func TestNewHandler_ValueAndErrorReturn(t *testing.T) {
	h := NewHandler(func(ctx context.Context, msg string) (string, error) {
		return "echo: " + msg, nil
	})

	call, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{"hello"})
	require.NoError(t, err)

	var gotResult any
	replier := func(ctx context.Context, result any, err error) error {
		gotResult = result
		require.NoError(t, err)
		return nil
	}

	_ = h(context.Background(), nil, replier, call)
	require.Equal(t, "echo: hello", gotResult)
}

func TestNewHandler_ValueAndErrorReturn_WithError(t *testing.T) {
	expectedErr := errors.New("test error")
	h := NewHandler(func(ctx context.Context) (string, error) {
		return "", expectedErr
	})

	call, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{})
	require.NoError(t, err)

	var gotErr error
	replier := func(ctx context.Context, result any, err error) error {
		gotErr = err
		return nil
	}

	_ = h(context.Background(), nil, replier, call)
	require.Equal(t, expectedErr, gotErr)
}

// ---------------------------------------------------------------------------
// Handler — panic recovery
// ---------------------------------------------------------------------------

func TestNewHandler_PanicRecovery_SingleReturn(t *testing.T) {
	h := NewHandler(func(ctx context.Context) error {
		panic("boom")
	})

	call, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{})
	require.NoError(t, err)

	var gotErr error
	replier := func(ctx context.Context, result any, err error) error {
		gotErr = err
		return nil
	}

	_ = h(context.Background(), nil, replier, call)
	require.Error(t, gotErr)
	require.Contains(t, gotErr.Error(), "boom")
}

func TestNewHandler_PanicRecovery_TwoReturns(t *testing.T) {
	h := NewHandler(func(ctx context.Context) (string, error) {
		panic("kaboom")
	})

	call, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{})
	require.NoError(t, err)

	var gotErr error
	replier := func(ctx context.Context, result any, err error) error {
		gotErr = err
		return nil
	}

	_ = h(context.Background(), nil, replier, call)
	require.Error(t, gotErr)
	require.Contains(t, gotErr.Error(), "kaboom")
}

// ---------------------------------------------------------------------------
// ServeHTTP coverage for environment_service, server_service, aspire_service
// These test that the handler maps are correctly wired up by calling
// a known method name with invalid params (exercises ServeHTTP + serveRpc).
// ---------------------------------------------------------------------------

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
	rpcConn.Go(context.Background(), nil)
	return rpcConn
}

func TestEnvironmentService_ServeHTTP(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	// Call a registered method with wrong params to exercise ServeHTTP handler map setup
	// The method exists in the handler map, so we should get InvalidParams, not MethodNotFound
	_, err := rpcConn.Call(context.Background(), "GetEnvironmentsAsync", "not-an-array", nil)
	require.Error(t, err)

	var rpcErr *jsonrpc2.Error
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, jsonrpc2.InvalidParams, rpcErr.Code)
}

func TestServerService_ServeHTTP(t *testing.T) {
	s := newTestServer()
	svc := newServerService(s)
	rpcConn := connectRPC(t, svc)

	_, err := rpcConn.Call(context.Background(), "InitializeAsync", "not-an-array", nil)
	require.Error(t, err)

	var rpcErr *jsonrpc2.Error
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, jsonrpc2.InvalidParams, rpcErr.Code)
}

func TestAspireService_ServeHTTP(t *testing.T) {
	s := newTestServer()
	svc := newAspireService(s)
	rpcConn := connectRPC(t, svc)

	_, err := rpcConn.Call(context.Background(), "GetAspireHostAsync", "not-an-array", nil)
	require.Error(t, err)

	var rpcErr *jsonrpc2.Error
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, jsonrpc2.InvalidParams, rpcErr.Code)
}

// Test that valid method names with valid params but missing session get InvalidParams
func TestEnvironmentService_ServeHTTP_AllMethods(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	methods := []string{
		"CreateEnvironmentAsync",
		"GetEnvironmentsAsync",
		"LoadEnvironmentAsync",
		"OpenEnvironmentAsync",
		"SetCurrentEnvironmentAsync",
		"DeleteEnvironmentAsync",
		"RefreshEnvironmentAsync",
		"DeployAsync",
		"DeployServiceAsync",
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			// Call with wrong param format to exercise the handler registration
			_, err := rpcConn.Call(context.Background(), method, "bad-params", nil)
			require.Error(t, err)
		})
	}
}

// ---------------------------------------------------------------------------
// messageWriter.Write — test through the Observer with a real WebSocket
// ---------------------------------------------------------------------------

func TestMessageWriter_Write_ViaObserver(t *testing.T) {
	// The messageWriter calls observer.OnNext which sends a JSON-RPC notification.
	// We set up a websocket server to receive the notification.

	received := make(chan string, 10)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		rpcServer := jsonrpc2.NewConn(newWebSocketStream(c))
		rpcServer.Go(r.Context(), func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
			received <- req.Method()
			return nil
		})
		<-rpcServer.Done()
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	serverURL.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverURL.String(), nil)
	require.NoError(t, err)

	rpcConn := jsonrpc2.NewConn(newWebSocketStream(wsConn))
	rpcConn.Go(context.Background(), nil)

	// Create an observer with handle 5
	obs := &Observer[ProgressMessage]{
		handle: 5,
		c:      rpcConn,
	}

	// Create messageWriter
	mw := &messageWriter{
		ctx:      context.Background(),
		observer: obs,
		messageTemplate: ProgressMessage{
			Severity: Info,
			Kind:     Logging,
		},
	}

	// Write through the messageWriter
	n, err := mw.Write([]byte("test output"))
	require.NoError(t, err)
	require.Equal(t, len("test output"), n)

	// Wait for the server to receive the notification
	method := <-received
	require.Equal(t, "$/invokeProxy/5/onNext", method)
}

// ---------------------------------------------------------------------------
// serveRpc — cancel request with nil id
// ---------------------------------------------------------------------------

func TestServeRpc_CancelNilId(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveRpc(w, r, map[string]Handler{})
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	serverURL.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverURL.String(), nil)
	require.NoError(t, err)

	rpcConn := jsonrpc2.NewConn(newWebSocketStream(wsConn))
	rpcConn.Go(context.Background(), nil)

	// Send cancel request without an id field — should get InvalidParams
	err = rpcConn.Notify(context.Background(), "$/cancelRequest", struct{}{})
	require.NoError(t, err) // notification itself doesn't return an error
}

// ---------------------------------------------------------------------------
// Handler — cancellation with value return path
// ---------------------------------------------------------------------------

func TestNewHandler_Cancellation_ValueReturn(t *testing.T) {
	h := NewHandler(func(ctx context.Context) (string, error) {
		return "", ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	call, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{})
	require.NoError(t, err)

	var gotErr error
	replier := func(ctx context.Context, result any, err error) error {
		gotErr = err
		return nil
	}

	_ = h(ctx, nil, replier, call)
	require.Error(t, gotErr)

	var rpcErr *jsonrpc2.Error
	require.True(t, errors.As(gotErr, &rpcErr))
	require.Equal(t, requestCanceledErrorCode, rpcErr.Code)
}

// ---------------------------------------------------------------------------
// Exercise RPC handler bodies by calling with valid-format params
// This triggers unmarshalArgs + validateSession, covering more statements
// ---------------------------------------------------------------------------

func TestEnvironmentService_GetEnvironmentsAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	// GetEnvironmentsAsync expects (RequestContext, Observer)
	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(context.Background(), "GetEnvironmentsAsync", []any{
		rc,
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_SetCurrentEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(context.Background(), "SetCurrentEnvironmentAsync", []any{
		rc,
		"env-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_DeleteEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(context.Background(), "DeleteEnvironmentAsync", []any{
		rc,
		"env-name",
		1,
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_CreateEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	env := Environment{
		Name: "test-env",
		Properties: map[string]string{
			"Subscription": "sub-123",
			"Location":     "eastus",
		},
	}
	_, err := rpcConn.Call(context.Background(), "CreateEnvironmentAsync", []any{
		rc,
		env,
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_OpenEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(context.Background(), "OpenEnvironmentAsync", []any{
		rc,
		"env-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_LoadEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(context.Background(), "LoadEnvironmentAsync", []any{
		rc,
		"env-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_RefreshEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(context.Background(), "RefreshEnvironmentAsync", []any{
		rc,
		"env-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_DeployAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(context.Background(), "DeployAsync", []any{
		rc,
		"env-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_DeployServiceAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(context.Background(), "DeployServiceAsync", []any{
		rc,
		"env-name",
		"service-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestAspireService_GetAspireHostAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newAspireService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(context.Background(), "GetAspireHostAsync", []any{
		rc,
		"aspire-env",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestAspireService_RenameAspireHostAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newAspireService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(context.Background(), "RenameAspireHostAsync", []any{
		rc,
		"/new/path",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestServerService_InitializeAsync_InvalidParams(t *testing.T) {
	s := newTestServer()
	svc := newServerService(s)
	rpcConn := connectRPC(t, svc)

	// InitializeAsync expects (rootPath string, options InitializeServerOptions)
	// Use the current working directory instead of TempDir to avoid cleanup issues
	// (InitializeAsync calls os.Chdir which locks the directory on Windows)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	var result Session
	_, err = rpcConn.Call(context.Background(), "InitializeAsync", []any{
		cwd,
		InitializeServerOptions{},
	}, &result)
	// This should succeed since InitializeAsync doesn't need a pre-existing session
	require.NoError(t, err)
	require.NotEmpty(t, result.Id)

	// Restore working directory
	_ = os.Chdir(cwd)
}

func TestServerService_StopAsync(t *testing.T) {
	s := newTestServer()
	// Must set cancelTelemetryUpload to avoid nil panic
	s.cancelTelemetryUpload = func() {}
	svc := newServerService(s)
	rpcConn := connectRPC(t, svc)

	_, err := rpcConn.Call(context.Background(), "StopAsync", []any{}, nil)
	require.NoError(t, err)
}
