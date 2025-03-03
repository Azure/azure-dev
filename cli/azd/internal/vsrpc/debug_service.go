// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
)

// debugService is the RPC server for the '/TestDebugService/v1.0' endpoint. It is only exposed when
// AZD_DEBUG_SERVER_DEBUG_ENDPOINTS is set to true as per [strconv.ParseBool]. It is also used by our
// unit tests.
type debugService struct {
	// When non-nil, TestCancelAsync will call `Done` on this wait group before waiting to observe
	// cancellation. This allows test code to orchestrate when it sends the cancellation message and to
	// know the RPC is ready to observe it.
	wg     *sync.WaitGroup
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
	if s.wg != nil {
		s.wg.Done()
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		return true, nil
	}
}

// TestIObserverAsync is the server implementation of:
// ValueTask<bool> TestIObserverAsync(int, CancellationToken);
//
// It emits a sequence of integers to the observer, from 0 to max, and then completes the observer, before returning.
func (s *debugService) TestIObserverAsync(ctx context.Context, max int, observer *Observer[int]) error {
	for i := 0; i < max; i++ {
		_ = observer.OnNext(ctx, i)
	}
	_ = observer.OnCompleted(ctx)
	return nil
}

// TestPanicAsync is the server implementation of:
// ValueTask<AccessToken> TestPanicAsync(string, CancellationToken);
//
// It causes a go `panic` with a given message string message.
func (s *debugService) TestPanicAsync(ctx context.Context, message string) error {
	panic(message)
}

// FetchTokenAsync is the server implementation of:
// ValueTask<AccessToken> FetchTokenAsync(Session, CancellationToken);
//
// It fetches an access token for the current user and returns it.
func (s *debugService) FetchTokenAsync(ctx context.Context, sessionId Session) (azcore.AccessToken, error) {
	session, err := s.server.validateSession(sessionId)
	if err != nil {
		return azcore.AccessToken{}, err
	}

	var c struct {
		credProvider auth.MultiTenantCredentialProvider `container:"type"`
	}

	container, err := session.newContainer(RequestContext{
		HostProjectPath: session.rootPath,
	})
	if err != nil {
		return azcore.AccessToken{}, err
	}

	if err := container.Fill(&c); err != nil {
		return azcore.AccessToken{}, err
	}

	cred, err := c.credProvider.GetTokenCredential(ctx, "")
	if err != nil {
		return azcore.AccessToken{}, err
	}

	return cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: auth.LoginScopes(cloud.AzurePublic()),
	})
}

// ServeHTTP implements http.Handler.
func (s *debugService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveRpc(w, r, map[string]Handler{
		"TestCancelAsync":    NewHandler(s.TestCancelAsync),
		"TestIObserverAsync": NewHandler(s.TestIObserverAsync),
		"TestPanicAsync":     NewHandler(s.TestPanicAsync),
		"FetchTokenAsync":    NewHandler(s.FetchTokenAsync),
	})
}
