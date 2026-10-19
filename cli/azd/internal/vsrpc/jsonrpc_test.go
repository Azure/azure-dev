// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"encoding/json"

	"go.lsp.dev/jsonrpc2"
)

func newCall(id jsonrpc2.ID, method string, params any) (*jsonrpc2.Call, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return jsonrpc2.NewCall(id, method, data), nil
}

func newNotification(method string, params any) (*jsonrpc2.Notification, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return jsonrpc2.NewNotification(method, data), nil
}

func invokeHandler(
	h Handler,
	ctx context.Context,
	conn jsonrpc2.Conn,
	validate func(context.Context, any, error) error,
	req *jsonrpc2.Call,
) error {
	result, err := h(ctx, conn, req.Method(), req.Params())
	return validate(ctx, result, err)
}
