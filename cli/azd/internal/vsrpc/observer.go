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

// connectionObserver is an interface that can be implemented by types that want to be notified when they are deserialized
// during a JSON RPC call.
type connectionObserver interface {
	attachConnection(c jsonrpc2.Conn)
}

// Observer is treated special by our JSON-RPC implementation and plays nicely with StreamJsonRpc's ideas on how to
// marshal an Observer<T> in .NET.
//
// The way this works is that that we can send a notification back to to the server with the method
// `$/invokeProxy/{handle}/{onCompleted|onNext}`. When marshalled as an argument, the wire format of Observer<T> is:
//
//	{
//	  "__jsonrpc_marshaled": 1,
//	  "handle": <some-integer>
//	}
type Observer[T any] struct {
	handle int
	c      jsonrpc2.Conn
}

func (o *Observer[T]) OnNext(ctx context.Context, value T) error {
	return o.c.Notify(ctx, fmt.Sprintf("$/invokeProxy/%d/onNext", o.handle), []any{value})
}

func (o *Observer[T]) OnCompleted(ctx context.Context) error {
	return o.c.Notify(ctx, fmt.Sprintf("$/invokeProxy/%d/onCompleted", o.handle), nil)
}

func (o *Observer[T]) UnmarshalJSON(data []byte) error {
	var wire map[string]int
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	if v, has := wire["__jsonrpc_marshaled"]; !has || v != 1 {
		return errors.New("expected __jsonrpc_marshaled=1")
	}

	if v, has := wire["handle"]; !has {
		return errors.New("expected handle")
	} else {
		o.handle = v
	}

	return nil
}

func (o *Observer[T]) attachConnection(c jsonrpc2.Conn) {
	o.c = c
}
