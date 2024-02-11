// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
)

func TestUnmarshalIObserver(t *testing.T) {
	t.Parallel()
	t.Run("Ok", func(t *testing.T) {
		req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{
			map[string]any{
				"__jsonrpc_marshaled": 1,
				"handle":              1,
			},
		})
		require.NoError(t, err)

		// As part of unmarshaling, we expect the IObserver to have it's connection parameter set to be set so it can send
		// messages back later. Create a new dummy jsonrpc2.Conn so we can validate this.
		con := jsonrpc2.NewConn(nil)

		o, err := unmarshalArg[IObserver[int]](con, req, 0)
		require.NoError(t, err)
		require.Equal(t, con, o.c, "rpc connection was not attached during unmarshaling!")
	})

	t.Run("No Tag", func(t *testing.T) {
		req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{
			map[string]any{
				"handle": 1,
			},
		})
		require.NoError(t, err)

		_, err = unmarshalArg[IObserver[int]](nil, req, 0)
		require.Error(t, err)
	})

	t.Run("Bad Tag", func(t *testing.T) {
		req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{
			map[string]any{
				"__jsonrpc_marshaled": 0,
				"handle":              1,
			},
		})
		require.NoError(t, err)

		_, err = unmarshalArg[IObserver[int]](nil, req, 0)
		require.Error(t, err)
	})

	t.Run("No Handle", func(t *testing.T) {
		req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{
			map[string]any{
				"__jsonrpc_marshaled": 1,
			},
		})
		require.NoError(t, err)

		_, err = unmarshalArg[IObserver[int]](nil, req, 0)
		require.Error(t, err)
	})

	t.Run("Bad Format", func(t *testing.T) {
		req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{
			"not-an-observer",
		})
		require.NoError(t, err)

		_, err = unmarshalArg[IObserver[int]](nil, req, 0)
		require.Error(t, err)
	})
}

// mockRpcConn implements the jsonrpc2.Conn interface just enough such that it can record any calls to [Notify]. This allows
// it to be used as the Conn for an [IObserver[T]] and can be used to validate any notifications that were sent during and
// RPC operatio
type mockRpcConn struct {
	// notifies is a log of all the calls to [Notify]. The first entry is the oldest call observed.
	notifies []struct {
		method string
		params interface{}
	}
}

// Call implements jsonrpc2.Conn
func (c *mockRpcConn) Call(ctx context.Context, method string, params interface{}, result interface{}) (jsonrpc2.ID, error) {
	panic("not implemented")
}

// Notify implements jsonrpc2.Conn
func (c *mockRpcConn) Notify(ctx context.Context, method string, params interface{}) error {
	c.notifies = append(c.notifies, struct {
		method string
		params interface{}
	}{method, params})
	return nil
}

// Go implements jsonrpc2.Conn
func (c *mockRpcConn) Go(ctx context.Context, handler jsonrpc2.Handler) {
	panic("not implemented")
}

// Close implements jsonrpc2.Conn
func (c *mockRpcConn) Close() error {
	panic("not implemented")
}

// Done implements jsonrpc2.Conn
func (c *mockRpcConn) Done() <-chan struct{} {
	panic("not implemented")
}

// Err implements jsonrpc2.Conn
func (c *mockRpcConn) Err() error {
	panic("not implemented")
}

// newLoggingObserver returns an IObserver[T] that records every message sent to it, and the [mockRpcConn] that can be used
// to view the log of these messages.
func newLoggingObserver[T any]() (IObserver[T], *mockRpcConn) {
	c := &mockRpcConn{}

	return IObserver[T]{c: c}, c
}
