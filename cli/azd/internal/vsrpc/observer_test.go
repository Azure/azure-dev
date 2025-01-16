package vsrpc

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
)

func TestUnmarshalObserver(t *testing.T) {
	fnType := reflect.TypeOf(func(ctx context.Context, o *Observer[int]) error { return nil })

	t.Parallel()
	t.Run("Ok", func(t *testing.T) {
		req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{
			map[string]any{
				"__jsonrpc_marshaled": 1,
				"handle":              1,
			},
		})
		require.NoError(t, err)

		// As part of unmarshaling, we expect the Observer to have it's connection parameter set to be set so it can send
		// messages back later. Create a new dummy jsonrpc2.Conn so we can validate this.
		con := jsonrpc2.NewConn(nil)

		args, err := unmarshalArgs(con, req, fnType)
		require.NoError(t, err)
		require.Len(t, args, 1)
		require.Equal(t, con, args[0].Interface().(*Observer[int]).c,
			"rpc connection was not attached during unmarshaling!")
	})

	t.Run("No Tag", func(t *testing.T) {
		req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{
			map[string]any{
				"handle": 1,
			},
		})
		require.NoError(t, err)

		_, err = unmarshalArgs(nil, req, fnType)
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

		_, err = unmarshalArgs(nil, req, fnType)
		require.Error(t, err)
	})

	t.Run("No Handle", func(t *testing.T) {
		req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{
			map[string]any{
				"__jsonrpc_marshaled": 1,
			},
		})
		require.NoError(t, err)

		_, err = unmarshalArgs(nil, req, fnType)
		require.Error(t, err)
	})

	t.Run("Bad Format", func(t *testing.T) {
		req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{
			"not-an-observer",
		})
		require.NoError(t, err)

		_, err = unmarshalArgs(nil, req, fnType)
		require.Error(t, err)
	})
}
