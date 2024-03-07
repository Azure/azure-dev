// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.lsp.dev/jsonrpc2"
)

const (
	// requestCanceledErrorCode is the error code that is used when a request is cancelled. StreamJsonRpc understands this
	// error code and causes the Task to throw a TaskCanceledException instead of a normal RemoteInvocationException error.
	requestCanceledErrorCode jsonrpc2.Code = -32800
)

// Handler is the type of function that handles incoming RPC requests.
type Handler func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error

// HandlerAction0 is a helper for creating a Handler from a function that takes no arguments and returns an error.
func HandlerAction0(f func(context.Context) error) Handler {
	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		err := f(ctx)
		if err != nil && errors.Is(err, ctx.Err()) {
			err = &jsonrpc2.Error{
				Code:    requestCanceledErrorCode,
				Message: err.Error(),
			}
		}
		return reply(ctx, nil, err)
	}
}

// HandlerAction1 is a helper for creating a Handler from a function that takes one argument and returns an error.
func HandlerAction1[T1 any](f func(context.Context, T1) error) Handler {
	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		t1, err := unmarshalArg[T1](conn, req, 0)
		if err != nil {
			return reply(ctx, nil, err)
		}

		err = f(ctx, t1)
		if err != nil && errors.Is(err, ctx.Err()) {
			err = &jsonrpc2.Error{
				Code:    requestCanceledErrorCode,
				Message: err.Error(),
			}
		}
		return reply(ctx, nil, err)
	}
}

// HandlerAction2 is a helper for creating a Handler from a function that takes two arguments and returns an error.
func HandlerAction2[T1 any, T2 any](f func(context.Context, T1, T2) error) Handler {
	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		t1, err := unmarshalArg[T1](conn, req, 0)
		if err != nil {
			return reply(ctx, nil, err)
		}

		t2, err := unmarshalArg[T2](conn, req, 1)
		if err != nil {
			return reply(ctx, nil, err)
		}

		err = f(ctx, t1, t2)
		if err != nil && errors.Is(err, ctx.Err()) {
			err = &jsonrpc2.Error{
				Code:    requestCanceledErrorCode,
				Message: err.Error(),
			}
		}
		return reply(ctx, nil, err)
	}
}

// HandlerAction3 is a helper for creating a Handler from a function that takes two arguments and returns an error.
func HandlerAction3[T1 any, T2 any, T3 any](f func(context.Context, T1, T2, T3) error) Handler {
	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		t1, err := unmarshalArg[T1](conn, req, 0)
		if err != nil {
			return reply(ctx, nil, err)
		}

		t2, err := unmarshalArg[T2](conn, req, 1)
		if err != nil {
			return reply(ctx, nil, err)
		}

		t3, err := unmarshalArg[T3](conn, req, 2)
		if err != nil {
			return reply(ctx, nil, err)
		}

		err = f(ctx, t1, t2, t3)
		if err != nil && errors.Is(err, ctx.Err()) {
			err = &jsonrpc2.Error{
				Code:    requestCanceledErrorCode,
				Message: err.Error(),
			}
		}
		return reply(ctx, nil, err)
	}
}

// HandlerFunc0 is a helper for creating a Handler from a function that takes no arguments and returns a value and an error.
func HandlerFunc0[TRet any](f func(context.Context) (TRet, error)) Handler {
	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		ret, err := f(ctx)
		if err != nil && errors.Is(err, ctx.Err()) {
			err = &jsonrpc2.Error{
				Code:    requestCanceledErrorCode,
				Message: err.Error(),
			}
		}
		return reply(ctx, ret, err)
	}
}

// HandlerFunc1 is a helper for creating a Handler from a function that takes one argument and returns a value and an error.
func HandlerFunc1[T1 any, TRet any](f func(context.Context, T1) (TRet, error)) Handler {
	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		t1, err := unmarshalArg[T1](conn, req, 0)
		if err != nil {
			return reply(ctx, nil, err)
		}

		ret, err := f(ctx, t1)
		if err != nil && errors.Is(err, ctx.Err()) {
			err = &jsonrpc2.Error{
				Code:    requestCanceledErrorCode,
				Message: err.Error(),
			}
		}
		return reply(ctx, ret, err)
	}
}

// HandlerFunc2 is a helper for creating a Handler from a function that takes two arguments and returns a value and an error.
func HandlerFunc2[T1 any, T2 any, TRet any](f func(context.Context, T1, T2) (TRet, error)) Handler {
	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		t1, err := unmarshalArg[T1](conn, req, 0)
		if err != nil {
			return reply(ctx, nil, err)
		}

		t2, err := unmarshalArg[T2](conn, req, 1)
		if err != nil {
			return reply(ctx, nil, err)
		}

		ret, err := f(ctx, t1, t2)
		if err != nil && errors.Is(err, ctx.Err()) {
			err = &jsonrpc2.Error{
				Code:    requestCanceledErrorCode,
				Message: err.Error(),
			}
		}
		return reply(ctx, ret, err)
	}
}

// HandlerFunc3 is a helper for creating a Handler from a function that takes three arguments and returns a value and an
// error.
func HandlerFunc3[T1 any, T2 any, T3 any, TRet any](f func(context.Context, T1, T2, T3) (TRet, error)) Handler {
	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		t1, err := unmarshalArg[T1](conn, req, 0)
		if err != nil {
			return reply(ctx, nil, err)
		}

		t2, err := unmarshalArg[T2](conn, req, 1)
		if err != nil {
			return reply(ctx, nil, err)
		}

		t3, err := unmarshalArg[T3](conn, req, 2)
		if err != nil {
			return reply(ctx, nil, err)
		}

		ret, err := f(ctx, t1, t2, t3)
		if err != nil && errors.Is(err, ctx.Err()) {
			err = &jsonrpc2.Error{
				Code:    requestCanceledErrorCode,
				Message: err.Error(),
			}
		}
		return reply(ctx, ret, err)
	}
}

// unmarshalArg returns the i'th member of the Params property of a request, after JSON unmarshalling it as an instance of T.
// If an error is returned, it is of type *jsonrpc.Error with a code of jsonrpc2.InvalidParams.
func unmarshalArg[T any](conn jsonrpc2.Conn, req jsonrpc2.Request, index int) (T, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(req.Params(), &args); err != nil {
		return *new(T), jsonrpc2.NewError(
			jsonrpc2.InvalidParams, fmt.Sprintf("unmarshalling params as array: %s", err.Error()))
	}

	if index >= len(args) {
		return *new(T), jsonrpc2.NewError(
			jsonrpc2.InvalidParams, fmt.Sprintf("param out of range, len: %d index: %d", len(args), index))
	}

	var arg T
	if err := json.Unmarshal(args[index], &arg); err != nil {
		return *new(T), jsonrpc2.NewError(
			jsonrpc2.InvalidParams, fmt.Sprintf("unmarshalling param: %s", err.Error()))
	}

	if v, ok := (any(&arg)).(connectionObserver); ok {
		v.attachConnection(conn)
	}

	return arg, nil
}
