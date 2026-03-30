// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
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
	id, session, err := s.server.newSession()
	if err != nil {
		return nil, err
	}

	if rootPath != "" {
		if fi, err := os.Stat(rootPath); err != nil {
			return nil, fmt.Errorf("invalid root path %q: %w", rootPath, err)
		} else if !fi.IsDir() {
			return nil, fmt.Errorf("root path %q is not a directory", rootPath)
		}
	}

	session.rootPath = rootPath
	session.rootContainer = s.server.rootContainer

	if options.AuthenticationEndpoint != nil {
		session.externalServicesEndpoint = *options.AuthenticationEndpoint
	}

	if options.AuthenticationKey != nil {
		session.externalServicesKey = *options.AuthenticationKey
	}

	if options.AuthenticationCertificate != nil {
		transport, err := httputil.TlsEnabledTransport(*options.AuthenticationCertificate)
		if err != nil {
			return nil, err
		}
		client := &http.Client{
			Transport: transport,
		}

		endpointUrl, err := url.Parse(session.externalServicesEndpoint)
		if err != nil {
			return nil, fmt.Errorf("invalid endpoint '%s': %w", session.externalServicesEndpoint, err)
		}

		if endpointUrl.Scheme != "https" {
			return nil,
				fmt.Errorf("invalid endpoint '%s': scheme must be 'https' when certificate is provided",
					session.externalServicesEndpoint)
		}

		session.externalServicesClient = client
	} else {
		session.externalServicesClient = http.DefaultClient
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
		"InitializeAsync": NewHandler(s.InitializeAsync),
		"StopAsync":       NewHandler(s.StopAsync),
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
