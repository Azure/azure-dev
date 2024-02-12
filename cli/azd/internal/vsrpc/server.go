// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/gorilla/websocket"
	"go.lsp.dev/jsonrpc2"
)

type Server struct {
	// sessions is a map of session IDs to server sessions.
	sessions map[string]*serverSession
	// sessionsMu protects access to sessions.
	sessionsMu sync.Mutex
	// rootContainer contains all the core registrations for the azd components. Each session creates a new scope from
	// this root container.
	rootContainer *ioc.NestedContainer
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
	// IObservers. This is useful for both developers unit testing in VS Code (where they can set this value in launch.json
	// as well as tests where we can set this value with t.SetEnv()).
	if on, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_SERVER_DEBUG_ENDPOINTS")); err == nil && on {
		mux.Handle("/TestDebugService/v1.0", newDebugService())
	}

	server := http.Server{
		ReadHeaderTimeout: 1 * time.Second,
		Handler:           mux,
	}
	return server.Serve(l)
}

// serveRpc upgrades the HTTP connection to a WebSocket connection and then serves a set of named method using JSON-RPC 2.0.
func serveRpc(w http.ResponseWriter, r *http.Request, handlers map[string]Handler) {
	log.Printf("serving rpc for %s", r.URL.Path)
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
		log.Printf("handling rpc for %s", req.Method())

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
			start := time.Now()

			defer func() {
				log.Printf("handled rpc for %s in %s", req.Method(), time.Since(start))
			}()
			var childCtx context.Context = ctx

			// If this is a call, create a new context and cancel function to track the request and allow it to be
			// canceled.
			call, isCall := req.(*jsonrpc2.Call)
			if isCall {
				ctx, cancel := context.WithCancel(ctx)
				childCtx = ctx
				cancelersMu.Lock()
				cancelers[call.ID()] = cancel
				cancelersMu.Unlock()
			}

			err := handler(childCtx, rpcServer, reply, req)

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

			if err != nil {
				log.Printf("handler for rpc %s returned error: %v", req.Method(), err)
			}
		}()

		return nil
	})

	<-rpcServer.Done()
}
