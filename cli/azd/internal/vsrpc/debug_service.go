// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"net/http"
	"time"
)

// debugService is the RPC server for the '/TestDebugService/v1.0' endpoint. It is only exposed when
// AZD_DEBUG_SERVER_DEBUG_ENDPOINTS is set to true as per [strconv.ParseBool].
type debugService struct {
	server *Server
}

func newDebugService(server *Server) *debugService {
	return &debugService{
		server: server,
	}
}

// TestCancelAsync is the server implementation of:
// ValueTask<bool> InitializeAsync(int, CancellationToken);
//
// It waits for the given timeoutMs, and then returns true. However, if the context is cancelled before the timeout,
// it returns false and ctx.Err() which should cause the client to throw a TaskCanceledException.
func (s *debugService) TestCancelAsync(ctx context.Context, timeoutMs int) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		return true, nil
	}
}

// TestCancelAsync is the server implementation of:
// ValueTask<bool> TestIObserver(int, CancellationToken);
//
// It emits a sequence of integers to the observer, from 0 to max, and then completes the observer, before returning.
func (s *debugService) TestIObserverAsync(ctx context.Context, max int, observer IObserver[int]) error {
	for i := 0; i < max; i++ {
		_ = observer.OnNext(ctx, i)
	}
	_ = observer.OnCompleted(ctx)
	return nil
}

// ServeHTTP implements http.Handler.
func (s *debugService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveRpc(w, r, map[string]Handler{
		"TestCancelAsync":    HandlerFunc1(s.TestCancelAsync),
		"TestIObserverAsync": HandlerAction2(s.TestIObserverAsync),
	})
}