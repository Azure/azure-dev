// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"

	"go.lsp.dev/jsonrpc2"
)

const (
	// requestCanceledErrorCode is the error code that is used when a request is cancelled. StreamJsonRpc understands this
	// error code and causes the Task to throw a TaskCanceledException instead of a normal RemoteInvocationException error.
	requestCanceledErrorCode jsonrpc2.Code = -32800
)

// Handler is the type of function that handles incoming RPC requests.
type Handler func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error

// NewHandler is a generic helper for creating a Handler from an arbitrary function. The function must:
//
// - Take a context.Context as its first argument.
// - Return either an error or a value and an error.
//
// If f does not meet these requirements, NewHandler will panic.
func NewHandler(f any) Handler {
	fnValue := reflect.ValueOf(f)
	fnType := fnValue.Type()

	if fnType.Kind() != reflect.Func {
		panic(fmt.Sprintf("NewHandler: expected a function, got %s", fnType.Kind()))
	}

	if fnType.NumIn() == 0 {
		panic("NewHandler: function must take at least one argument")
	}

	if fnType.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
		panic(fmt.Sprintf("NewHandler: first argument must be a context.Context, got %s", fnType.In(0)))
	}

	if fnType.NumOut() != 1 && fnType.NumOut() != 2 {
		panic(fmt.Sprintf("NewHandler: function must return either an error or a value and an error, got %d return values",
			fnType.NumOut()))
	}

	if fnType.NumOut() == 1 && fnType.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
		panic(fmt.Sprintf("NewHandler: single return value must be an error, got %s", fnType.Out(0)))
	}

	if fnType.NumOut() == 2 && fnType.Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
		panic(fmt.Sprintf("NewHandler: second return value must be an error, got %s", fnType.Out(1)))
	}

	return func(ctx context.Context, conn jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		args, err := unmarshalArgs(conn, req, fnType)
		if err != nil {
			return reply(ctx, nil, err)
		}

		var results []reflect.Value
		func() {
			defer func() {
				if p := recover(); p != nil {
					stackBuf := make([]byte, 4096)
					stack := string(stackBuf[:runtime.Stack(stackBuf, false)])

					rpcErr := &jsonrpc2.Error{
						Code:    jsonrpc2.InternalError,
						Message: fmt.Sprintf("panic: %v\n%v", p, stack),
					}

					if fnType.NumOut() == 1 {
						results = []reflect.Value{reflect.ValueOf(rpcErr)}
					} else if fnType.NumOut() == 2 {
						results = []reflect.Value{reflect.Zero(fnType.Out(0)), reflect.ValueOf(rpcErr)}
					}
				}
			}()

			results = fnValue.Call(append([]reflect.Value{reflect.ValueOf(ctx)}, args...))
		}()

		if fnType.NumOut() == 1 {
			// Function returns only an error
			errResult := results[0].Interface()
			if errResult != nil {
				err = errResult.(error)
			}
			if err != nil && errors.Is(err, ctx.Err()) {
				err = &jsonrpc2.Error{
					Code:    requestCanceledErrorCode,
					Message: err.Error(),
				}
			}
			return reply(ctx, nil, err)
		} else if fnType.NumOut() == 2 {
			// Function returns a value and an error
			ret := results[0].Interface()
			errResult := results[1].Interface()
			if errResult != nil {
				err = errResult.(error)
			}
			if err != nil && errors.Is(err, ctx.Err()) {
				err = &jsonrpc2.Error{
					Code:    requestCanceledErrorCode,
					Message: err.Error(),
				}
			}
			return reply(ctx, ret, err)
		}
		return reply(ctx, nil, fmt.Errorf("unexpected number of return values"))
	}
}

// unmarshalArgs unmarshals the request parameters into the expected argument types for the function.
func unmarshalArgs(conn jsonrpc2.Conn, req jsonrpc2.Request, fnType reflect.Type) ([]reflect.Value, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(req.Params(), &args); err != nil {
		return nil, jsonrpc2.NewError(
			jsonrpc2.InvalidParams, fmt.Sprintf("unmarshalling params as array: %s", err.Error()))
	}

	// The first argument in the go handler function is always a context, and that argument is not passed on the wire.
	expectedArgs := fnType.NumIn() - 1

	if len(args) != expectedArgs {
		return nil, jsonrpc2.NewError(
			jsonrpc2.InvalidParams, fmt.Sprintf("expected %d params for %s, got %d", expectedArgs, req.Method(), len(args)))
	}

	fnArgs := make([]reflect.Value, expectedArgs)
	for i := 0; i < len(args); i++ {
		argType := fnType.In(i + 1)
		argValue := reflect.New(argType).Interface()
		if err := json.Unmarshal(args[i], argValue); err != nil {
			return nil, jsonrpc2.NewError(
				jsonrpc2.InvalidParams, fmt.Sprintf("unmarshalling param: %s", err.Error()))
		}
		fnArgs[i] = reflect.ValueOf(argValue).Elem()

		if v, ok := (fnArgs[i].Interface()).(connectionObserver); ok {
			v.attachConnection(conn)
		}
	}

	return fnArgs, nil
}
