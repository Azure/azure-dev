package vsrpc

import (
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
