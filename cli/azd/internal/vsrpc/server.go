// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/gorilla/websocket"
	"go.lsp.dev/jsonrpc2"
)

type Server struct {
	// sessions is a map of session IDs to server sessions.
	sessions map[string]*serverSession
	// sessionsMu protects access to sessions.
	sessionsMu sync.Mutex
	// rootContainer contains all the core registrations for the azd components.
	// It is not expected to be modified throughout the lifetime of the server.
	rootContainer *ioc.NestedContainer
	// cancelTelemetryUpload is a function that cancels the background telemetry upload goroutine.
	cancelTelemetryUpload func()
}

func NewServer(rootContainer *ioc.NestedContainer) *Server {
	return &Server{
		sessions:      make(map[string]*serverSession),
		rootContainer: rootContainer,
	}
}

// upgrader is the websocket.Upgrader used by the server to upgrade each request to a websocket connection.
var upgrader = websocket.Upgrader{}

// Serve calls http.Serve with the given listener and a handler that serves the VS RPC protocol.
func (s *Server) Serve(l net.Listener) error {
	mux := http.NewServeMux()

	mux.Handle("/AspireService/v1.0", newAspireService(s))
	mux.Handle("/ServerService/v1.0", newServerService(s))
	mux.Handle("/EnvironmentService/v1.0", newEnvironmentService(s))

	// Expose a few special test endpoints that can be used to debug our special RPC behavior around cancellation and
	// observers. This is useful for both developers unit testing in VS Code (where they can set this value in launch.json
	// as well as tests where we can set this value with t.SetEnv()).
	if on, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_SERVER_DEBUG_ENDPOINTS")); err == nil && on {
		mux.Handle("/TestDebugService/v1.0", newDebugService(s))
	}

	// Run upload periodically in the background while the server is running.
	ctx, cancel := context.WithCancel(context.Background())
	ts := telemetry.GetTelemetrySystem()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		for {
			err := ts.RunBackgroundUpload(ctx, false)
			if err != nil {
				log.Printf("telemetry upload failed: %v", err)
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	s.cancelTelemetryUpload = cancel

	server := http.Server{
		ReadHeaderTimeout: 1 * time.Second,
		Handler:           mux,
	}

	return server.Serve(l)
}

// serveRpc upgrades the HTTP connection to a WebSocket connection and then serves a set of named method using JSON-RPC 2.0.
func serveRpc(w http.ResponseWriter, r *http.Request, handlers map[string]Handler) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()

	rpcServer := jsonrpc2.NewConn(newWebSocketStream(c))
	cancelers := make(map[jsonrpc2.ID]context.CancelFunc)
	cancelersMu := sync.Mutex{}

	rpcServer.Go(r.Context(), func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		log.Printf("handling rpc %s", req.Method())

		// Observe cancellation messages from the client to us. The protocol is a message sent to the `$/cancelRequest`
		// method with an `id` parameter that is the ID of the request to cancel.  For each inflight RPC we track the
		// corresponding cancel function in `cancelers` and use it to cancel the request when we observe it. When an RPC
		// completes, we remove the cancel function from the map and invoke it, as the context package recommends.
		//
		// Inside the handler itself, we observe the error returned by the actual RPC function and if it matches the
		// value of ctx.Err() we know that the function observed an responded to cancellation. We then return an RPC error
		// with a special code that StreamJsonRpc understands as a cancellation error.
		if req.Method() == "$/cancelRequest" {
			var cancelArgs struct {
				Id *int32 `json:"id"`
			}
			if err := json.Unmarshal(req.Params(), &cancelArgs); err != nil {
				return reply(ctx, nil, jsonrpc2.ErrInvalidParams)
			}
			if cancelArgs.Id == nil {
				return reply(ctx, nil, jsonrpc2.ErrInvalidParams)
			}

			id := jsonrpc2.NewNumberID(*cancelArgs.Id)

			cancelersMu.Lock()
			cancel, has := cancelers[id]
			cancelersMu.Unlock()
			if has {
				cancel()
				// We'll remove the cancel function from the map once the function observes cancellation and the handler
				// returns, no need to remove it eagerly here.
			}
			return reply(ctx, nil, nil)
		}

		handler, ok := handlers[req.Method()]
		if !ok {
			return reply(ctx, nil, jsonrpc2.ErrMethodNotFound)
		}

		go func() {
			var respErr error
			childCtx, span := tracing.Start(ctx, events.VsRpcEventPrefix+req.Method())
			span.SetAttributes(fields.RpcMethod.String(req.Method()))
			defer func() {
				if respErr != nil {
					cmd.MapError(respErr, span)
					var rpcErr *jsonrpc2.Error
					if errors.As(respErr, &rpcErr) {
						span.SetAttributes(fields.JsonRpcErrorCode.Int(int(rpcErr.Code)))
					}
				}

				// Include any usage attributes set
				span.SetAttributes(tracing.GetUsageAttributes()...)
				span.End()
			}()

			// Wrap the reply function to capture the response error returned by the handler before replying.
			origReply := reply
			reply = func(ctx context.Context, result interface{}, err error) error {
				if err != nil {
					respErr = err
				}
				return origReply(ctx, result, err)
			}

			start := time.Now()

			// If this is a call, create a new context and cancel function to track the request and allow it to be
			// canceled.
			call, isCall := req.(*jsonrpc2.Call)
			if isCall {
				span.SetAttributes(fields.JsonRpcId.String(fmt.Sprint(call.ID())))
				ctx, cancel := context.WithCancel(ctx)
				childCtx = ctx
				cancelersMu.Lock()
				cancelers[call.ID()] = cancel
				cancelersMu.Unlock()
			}

			replyErr := handler(childCtx, rpcServer, reply, req)

			if isCall {
				cancelersMu.Lock()
				cancel, has := cancelers[call.ID()]
				cancelersMu.Unlock()
				if has {
					cancel()
					cancelersMu.Lock()
					delete(cancelers, call.ID())
					cancelersMu.Unlock()
				}
			}

			if respErr != nil {
				log.Printf("handled rpc %s in %s with err: %v", req.Method(), time.Since(start), respErr)
			} else if replyErr != nil {
				log.Printf("failed to reply to rpc %s, err: %v", req.Method(), respErr)
			} else {
				log.Printf("handled rpc %s in %s", req.Method(), time.Since(start))
			}
		}()

		return nil
	})

	<-rpcServer.Done()
}
