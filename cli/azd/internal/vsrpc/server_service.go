// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
)

// serverService is the RPC server for the '/ServerService/v1.0' endpoint.
type serverService struct {
	server *Server
}

func newServerService(server *Server) *serverService {
	return &serverService{
		server: server,
	}
}

// InitializeAsync is the server implementation of:
// ValueTask<Session> InitializeAsync(string rootPath, InitializeServerOptions options, CancellationToken cancellationToken);
func (s *serverService) InitializeAsync(
	ctx context.Context, rootPath string, options InitializeServerOptions,
) (*Session, error) {
	// TODO(azure/azure-dev#3288): Ideally the Chdir would be be something we injected into components instead of it being
	// ambient authority. We'll get there, but for now let's also just Chdir into the root folder so places where we use
	// a relative path will work.
	//
	// In practice we do not expect multiple clients with different root paths to be calling into the same server. If you
	// need that today, launch a new server for each root path...
	if err := os.Chdir(rootPath); err != nil {
		return nil, err
	}

	id, session, err := s.server.newSession()
	if err != nil {
		return nil, err
	}

	session.rootPath = rootPath
	session.rootContainer = s.server.rootContainer

	if options.AuthenticationEndpoint != nil {
		session.externalServicesEndpoint = *options.AuthenticationEndpoint
	}

	if options.AuthenticationKey != nil {
		session.externalServicesKey = *options.AuthenticationKey
	}

	return &Session{
		Id: id,
	}, nil
}

// StopAsync is the server implementation of:
// ValueTask StopAsync(CancellationToken cancellationToken);
func (s *serverService) StopAsync(ctx context.Context) error {
	// TODO(azure/azure-dev#3286): Need to think about how shutdown works. For now it is probably best to just have the
	// client terminate `azd` once they know all outstanding RPCs have completed instead of trying to do a graceful
	// shutdown on our end.

	ts := telemetry.GetTelemetrySystem()
	// Flush all in-memory telemetry data before stopping.
	err := ts.Shutdown(ctx)
	if err != nil {
		log.Printf("error shutting down telemetry: %v", err)
	}

	// Graceful telemetry cancellation.
	// This is not strictly necessary, but it is a good practice to cancel the telemetry upload before shutting down.
	s.server.cancelTelemetryUpload()

	return nil
}

// ServeHTTP implements http.Handler.
func (s *serverService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveRpc(w, r, map[string]Handler{
		"InitializeAsync": HandlerFunc2(s.InitializeAsync),
		"StopAsync":       HandlerAction0(s.StopAsync),
	})
}

// newWriter returns a *writerMultiplexer that has a default writer that writes to log.Printf with the given prefix.
func newWriter(prefix string) *writerMultiplexer {
	wm := &writerMultiplexer{}
	wm.AddWriter(writerFunc(func(p []byte) (n int, err error) {
		log.Printf("%s%s", prefix, string(p))
		return n, nil
	}))

	return wm
}
