// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"

	"go.lsp.dev/jsonrpc2"
)

const (
	// requestCanceledErrorCode is the error code that is used when a request is cancelled. StreamJsonRpc understands this
	// error code and causes the Task to throw a TaskCanceledException instead of a normal RemoteInvocationException error.
	requestCanceledErrorCode jsonrpc2.Code = -32800

	// arityZero is the count of arguments in a zero argument function.
	arityZero = 0
	// arityOne is the count of arguments in an one argument function.
	arityOne = 1
	// arityTwo is the count of arguments in a two argument function.
	arityTwo = 2
	// arityThree is the count of arguments in a three argument function.
	arityThree = 3
	// arityFour is the count of arguments in a four argument function.
	arityFour = 4
)

// Handler is the type of function that handles incoming RPC requests.
type Handler func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error

// HandlerAction0 is a helper for creating a Handler from a function that takes no arguments and returns an error.
func HandlerAction0(f func(context.Context) error) Handler {
	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		if err := verifyArity(req, arityZero); err != nil {
			return reply(ctx, nil, err)
		}

		err := func() (err error) {
			defer capturePanic(&err)
			err = f(ctx)
			return
		}()

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
		if err := verifyArity(req, arityOne); err != nil {
			return reply(ctx, nil, err)
		}

		t1, err := unmarshalArg[T1](conn, req, 0)
		if err != nil {
			return reply(ctx, nil, err)
		}

		err = func() (err error) {
			defer capturePanic(&err)
			err = f(ctx, t1)
			return
		}()

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
		if err := verifyArity(req, arityTwo); err != nil {
			return reply(ctx, nil, err)
		}

		t1, err := unmarshalArg[T1](conn, req, 0)
		if err != nil {
			return reply(ctx, nil, err)
		}

		t2, err := unmarshalArg[T2](conn, req, 1)
		if err != nil {
			return reply(ctx, nil, err)
		}

		err = func() (err error) {
			defer capturePanic(&err)
			err = f(ctx, t1, t2)
			return
		}()

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
		if err := verifyArity(req, arityThree); err != nil {
			return reply(ctx, nil, err)
		}

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

		err = func() (err error) {
			defer capturePanic(&err)
			err = f(ctx, t1, t2, t3)
			return
		}()

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
		if err := verifyArity(req, arityZero); err != nil {
			return reply(ctx, nil, err)
		}

		ret, err := func() (ret TRet, err error) {
			defer capturePanic(&err)
			ret, err = f(ctx)
			return
		}()

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
		if err := verifyArity(req, arityOne); err != nil {
			return reply(ctx, nil, err)
		}

		t1, err := unmarshalArg[T1](conn, req, 0)
		if err != nil {
			return reply(ctx, nil, err)
		}

		ret, err := func() (ret TRet, err error) {
			defer capturePanic(&err)
			ret, err = f(ctx, t1)
			return
		}()

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
		if err := verifyArity(req, arityTwo); err != nil {
			return reply(ctx, nil, err)
		}

		t1, err := unmarshalArg[T1](conn, req, 0)
		if err != nil {
			return reply(ctx, nil, err)
		}

		t2, err := unmarshalArg[T2](conn, req, 1)
		if err != nil {
			return reply(ctx, nil, err)
		}

		ret, err := func() (ret TRet, err error) {
			defer capturePanic(&err)
			ret, err = f(ctx, t1, t2)
			return
		}()

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
		if err := verifyArity(req, arityThree); err != nil {
			return reply(ctx, nil, err)
		}

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

		ret, err := func() (ret TRet, err error) {
			defer capturePanic(&err)
			ret, err = f(ctx, t1, t2, t3)
			return
		}()

		if err != nil && errors.Is(err, ctx.Err()) {
			err = &jsonrpc2.Error{
				Code:    requestCanceledErrorCode,
				Message: err.Error(),
			}
		}
		return reply(ctx, ret, err)
	}
}

// HandlerFunc4 is a helper for creating a Handler from a function that takes four arguments and returns a value and an
// error.
func HandlerFunc4[T1 any, T2 any, T3 any, T4 any, TRet any](f func(context.Context, T1, T2, T3, T4) (TRet, error)) Handler {
	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		if err := verifyArity(req, arityFour); err != nil {
			return reply(ctx, nil, err)
		}

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

		t4, err := unmarshalArg[T4](conn, req, 3)
		if err != nil {
			return reply(ctx, nil, err)
		}

		ret, err := func() (ret TRet, err error) {
			defer capturePanic(&err)
			ret, err = f(ctx, t1, t2, t3, t4)
			return
		}()

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

// verifyArity returns a error if the number of parameters in a request does not match the expected number. The error
// is of type *jsonrpc2.Error with a code of jsonrpc2.InvalidParams.
func verifyArity(req jsonrpc2.Request, expected int) error {
	var args []json.RawMessage
	if err := json.Unmarshal(req.Params(), &args); err != nil {
		return jsonrpc2.NewError(
			jsonrpc2.InvalidParams, fmt.Sprintf("unmarshalling params as array: %s", err.Error()))
	}

	if len(args) != expected {
		return jsonrpc2.NewError(
			jsonrpc2.InvalidParams, fmt.Sprintf("expected %d params for %s, got %d", expected, req.Method(), len(args)))
	}

	return nil
}

// capturePanic is a helper for capturing panics and converting them to an error. It is expected to be called via `defer`:
//
//	err := func() (err error) {
//		defer capturePanic(&err)
//		err = /* ... some call that might panic ... */
//		return
//	}()
//
// If a panic occurs, the error will be set to a new *jsonrpc2.Error instance with a code of jsonrpc2.InternalError and a
// message that includes the panic value.
func capturePanic(err *error) {
	if p := recover(); p != nil {
		stackBuf := make([]byte, 4096)
		stack := string(stackBuf[:runtime.Stack(stackBuf, false)])

		*err = &jsonrpc2.Error{
			Code:    jsonrpc2.InternalError,
			Message: fmt.Sprintf("panic: %v\n%v", p, stack),
		}
	}
}
