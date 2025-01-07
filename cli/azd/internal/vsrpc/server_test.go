package vsrpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
)

func TestArity(t *testing.T) {
	debugServer := httptest.NewServer(newDebugService(nil))
	defer debugServer.Close()

	// Connect to the server and start running a JSON-RPC 2.0 connection so we can send and recieve messages.
	serverUrl, err := url.Parse(debugServer.URL)
	require.NoError(t, err)
	serverUrl.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverUrl.String(), nil)
	require.NoError(t, err)

	rpcConn := jsonrpc2.NewConn(newWebSocketStream(wsConn))
	rpcConn.Go(context.Background(), nil)

	var rpcErr *jsonrpc2.Error

	// TestIObserverAsync expects two argumments - this call should fail, there are too few arguments.
	_, err = rpcConn.Call(context.Background(), "TestIObserverAsync", []any{10}, nil)
	require.Error(t, err)
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, jsonrpc2.InvalidParams, rpcErr.Code)

	// TestIObserverAsync expects two argumments - this call should fail, there are too many arguments.
	_, err = rpcConn.Call(context.Background(), "TestIObserverAsync", []any{10, map[string]any{
		"__jsonrpc_marshaled": 1,
		"handle":              1,
	}, "extra-argument"}, nil)
	require.Error(t, err)
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, jsonrpc2.InvalidParams, rpcErr.Code)
}

func TestCancellation(t *testing.T) {
	// wg controls when cancellation is sent by the client. We wait until the server RPC has started
	// to run before requesting cancellation so we ensure we are testing our logic.
	var wg sync.WaitGroup
	wg.Add(1)

	debugService := newDebugService(nil)
	debugService.wg = &wg
	debugServer := httptest.NewServer(debugService)
	defer debugServer.Close()

	// Connect to the server and start running a JSON-RPC 2.0 connection so we can send and recieve messages.
	serverUrl, err := url.Parse(debugServer.URL)
	require.NoError(t, err)
	serverUrl.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverUrl.String(), nil)
	require.NoError(t, err)

	rpcConn := jsonrpc2.NewConn(newWebSocketStream(wsConn))
	rpcConn.Go(context.Background(), nil)

	// Call blocks until the response is returned from the server, so spin off a goroutine that will make
	// the call and the shuttle the response back to us.
	result := make(chan struct {
		res bool
		err error
	})

	go func() {
		var res bool
		_, err := rpcConn.Call(context.Background(), "TestCancelAsync", []any{10000}, &res)
		result <- struct {
			res bool
			err error
		}{res, err}
		close(result)
	}()

	// Wait until the server starts processing the RPC, then request it be cancelled. We know the
	// id of the inflight call is 1 because the jsonrpc2 package assigns ids starting at 1.
	wg.Wait()
	err = rpcConn.Notify(context.Background(), "$/cancelRequest", struct {
		Id int `json:"id"`
	}{Id: 1})
	require.NoError(t, err)

	// Now, wait for the RPC to either complete (if we have a bug) or to observe cancellation and have
	// the results sent back here.
	res := <-result
	var rpcErr *jsonrpc2.Error

	require.False(t, res.res, "call should have been aborted, and returned false")
	require.True(t, errors.As(res.err, &rpcErr))
	require.Equal(t, requestCanceledErrorCode, rpcErr.Code)
}

func TestObserverable(t *testing.T) {
	debugServer := httptest.NewServer(newDebugService(nil))
	defer debugServer.Close()

	// Connect to the server and start running a JSON-RPC 2.0 connection so we can send and recieve messages.
	serverUrl, err := url.Parse(debugServer.URL)
	require.NoError(t, err)
	serverUrl.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverUrl.String(), nil)
	require.NoError(t, err)

	// The Observer machinary ends up sending RPCs back to the client, capture them so we can validate they are
	// correct later.
	var onNextParams []json.RawMessage
	var onCompletedParams []json.RawMessage

	rpcConn := jsonrpc2.NewConn(newWebSocketStream(wsConn))
	rpcConn.Go(context.Background(), func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		switch req.Method() {
		case "$/invokeProxy/1/onNext":
			onNextParams = append(onNextParams, req.Params())
		case "$/invokeProxy/1/onCompleted":
			onCompletedParams = append(onCompletedParams, req.Params())
		default:
			require.Fail(t, "unexpected rpc %s delivered", req.Method())
		}

		return nil
	})

	// The second argument is the wire form of an IObserver as marshalled by StreamJsonRpc. We use the handle when sending
	// messages back to the client.
	args := []any{
		10,
		map[string]any{
			"__jsonrpc_marshaled": 1,
			"handle":              1,
		},
	}

	require.NoError(t, err)

	_, err = rpcConn.Call(context.Background(), "TestIObserverAsync", args, nil)
	require.NoError(t, err)

	require.Len(t, onNextParams, 10)
	require.Len(t, onCompletedParams, 1)

	// Ensure the correct integers were sent back in the correct order, this should match
	// the order the were emited by the server.
	for idx, params := range onNextParams {
		var args []int
		require.NoError(t, json.Unmarshal(params, &args))
		require.Len(t, args, 1)
		require.Equal(t, idx, args[0])
	}

	// The onCompleted message takes no parameters and the args value is empty.
	require.Len(t, onCompletedParams[0], 0)
}

func TestPanic(t *testing.T) {
	debugServer := httptest.NewServer(newDebugService(nil))
	defer debugServer.Close()

	// Connect to the server and start running a JSON-RPC 2.0 connection so we can send and recieve messages.
	serverUrl, err := url.Parse(debugServer.URL)
	require.NoError(t, err)
	serverUrl.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverUrl.String(), nil)
	require.NoError(t, err)

	rpcConn := jsonrpc2.NewConn(newWebSocketStream(wsConn))
	rpcConn.Go(context.Background(), nil)

	_, err = rpcConn.Call(context.Background(), "TestPanicAsync", []any{"this is the panic office."}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "this is the panic office.")

	// Ensure the server is still running and we can make another call.
	_, err = rpcConn.Call(context.Background(), "TestPanicAsync", []any{"this is the panic office, again."}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "this is the panic office, again.")
}
