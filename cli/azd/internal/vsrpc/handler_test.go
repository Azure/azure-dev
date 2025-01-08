package vsrpc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
)

// newHandlerForCaseFunc is a function that constructs a new Handler for a given test case.
type newHandlerForCaseFunc func(t *testing.T, tc handlerTestCase) Handler

type handlerTestCase struct {
	// name is the name of the test, as passed to (*testing.T).Run().
	name string
	// expected is the result the handler should return on success.
	expected any
	// err is the error returned by the handler when non nil.
	err error
	// cancel is true when the handler should be cancelled.
	cancel bool
	// params are the params that are passed to the call, this should be an slice of values.
	params []any
}

// runHandlerSuite runs through a suite of tests for a handler. It exercises cases where the handler
// returns a value, returns and error not notices it has been cancelled and returns a special error to
// the caller.
func runHandlerSuite(t *testing.T, newHandler newHandlerForCaseFunc, params []any, expected any) {
	cases := []handlerTestCase{
		{
			name:     "Success",
			expected: expected,
			params:   params,
		},
		{
			name:   "Error",
			err:    errors.New("expected error"),
			params: params,
		},
		{
			name:   "Canceled",
			cancel: true,
			params: params,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHandler(t, tc)

			call, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", tc.params)
			require.NoError(t, err)

			ctx := context.Background()
			if tc.err != nil {
				_ = h(ctx, nil, validateError(t, tc.err), call)
			} else if tc.cancel {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				_ = h(ctx, nil, validateCancel(t), call)
			} else {
				_ = h(ctx, nil, validateResult(t, tc.expected), call)
			}
		})
	}
}

func TestHandler(t *testing.T) {
	t.Parallel()
	t.Run("HandlerAction0", func(t *testing.T) { runHandlerSuite(t, newHandlerAction0, nil, nil) })
	t.Run("HandlerAction1", func(t *testing.T) { runHandlerSuite(t, newHandlerAction1, []any{"arg0"}, nil) })
	t.Run("HandlerAction2", func(t *testing.T) { runHandlerSuite(t, newHandlerAction2, []any{"arg0", "arg1"}, nil) })
	t.Run("HandlerAction3", func(t *testing.T) { runHandlerSuite(t, newHandlerAction3, []any{"arg0", "arg1", "arg2"}, nil) })
	t.Run("HandlerFunc0", func(t *testing.T) { runHandlerSuite(t, newHandlerFunc0, nil, "ok") })
	t.Run("HandlerFunc1", func(t *testing.T) { runHandlerSuite(t, newHandlerFunc1, []any{"arg0"}, "ok") })
	t.Run("HandlerFunc2", func(t *testing.T) { runHandlerSuite(t, newHandlerFunc2, []any{"arg0", "arg1"}, "ok") })
	t.Run("HandlerFunc3", func(t *testing.T) { runHandlerSuite(t, newHandlerFunc3, []any{"arg0", "arg1", "arg2"}, "ok") })
}

func newHandlerAction0(t *testing.T, tc handlerTestCase) Handler {
	return NewHandler(func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return tc.err
		}
	})
}

func newHandlerAction1(t *testing.T, tc handlerTestCase) Handler {
	return NewHandler(func(ctx context.Context, arg0 string) error {
		validateParam(t, tc.params, 0, arg0)

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return tc.err
		}
	})
}

func newHandlerAction2(t *testing.T, tc handlerTestCase) Handler {
	return NewHandler(func(ctx context.Context, arg0, arg1 string) error {
		validateParam(t, tc.params, 0, arg0)
		validateParam(t, tc.params, 1, arg1)

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return tc.err
		}
	})
}

func newHandlerAction3(t *testing.T, tc handlerTestCase) Handler {
	return NewHandler(func(ctx context.Context, arg0, arg1, arg2 string) error {
		validateParam(t, tc.params, 0, arg0)
		validateParam(t, tc.params, 1, arg1)
		validateParam(t, tc.params, 2, arg2)

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return tc.err
		}
	})
}

func newHandlerFunc0(t *testing.T, tc handlerTestCase) Handler {
	return NewHandler(func(ctx context.Context) (any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return tc.expected, tc.err
		}
	})
}

func newHandlerFunc1(t *testing.T, tc handlerTestCase) Handler {
	return NewHandler(func(ctx context.Context, arg0 string) (any, error) {
		validateParam(t, tc.params, 0, arg0)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return tc.expected, tc.err
		}
	})
}

func newHandlerFunc2(t *testing.T, tc handlerTestCase) Handler {
	return NewHandler(func(ctx context.Context, arg0, arg1 string) (any, error) {
		validateParam(t, tc.params, 0, arg0)
		validateParam(t, tc.params, 1, arg1)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return tc.expected, tc.err
		}
	})
}

func newHandlerFunc3(t *testing.T, tc handlerTestCase) Handler {
	return NewHandler(func(ctx context.Context, arg0, arg1, arg2 string) (any, error) {
		validateParam(t, tc.params, 0, arg0)
		validateParam(t, tc.params, 1, arg1)
		validateParam(t, tc.params, 2, arg2)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return tc.expected, tc.err
		}
	})
}

func validateParam(t *testing.T, params any, n int, expected any) {
	require.IsType(t, []any(nil), params)
	args := params.([]any)
	require.GreaterOrEqual(t, len(args), n)
	require.Equal(t, expected, args[n])
}

func validateError(t *testing.T, expected error) jsonrpc2.Replier {
	return func(ctx context.Context, result any, err error) error {
		require.Nil(t, result)
		require.Equal(t, expected, err)
		return nil
	}
}

func validateCancel(t *testing.T) jsonrpc2.Replier {
	return func(ctx context.Context, result any, err error) error {
		require.Nil(t, result)
		var rpcErr *jsonrpc2.Error
		require.True(t, errors.As(err, &rpcErr))
		require.Equal(t, requestCanceledErrorCode, rpcErr.Code)
		return nil
	}
}

func validateResult(t *testing.T, expected any) jsonrpc2.Replier {
	return func(ctx context.Context, result any, err error) error {
		require.Nil(t, err)
		require.Equal(t, expected, result)
		return nil
	}
}
