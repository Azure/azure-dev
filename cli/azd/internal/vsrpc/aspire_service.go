// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

// aspireService is the RPC server for the '/AspireService/v1.0' endpoint.
type aspireService struct {
	server *Server
}

func newAspireService(server *Server) *aspireService {
	return &aspireService{
		server: server,
	}
}

// GetAspireHostAsync is the server implementation of:
// ValueTask<AspireHost> GetAspireHostAsync(Session session, string aspireEnv, CancellationToken cancellationToken).
func (s *aspireService) GetAspireHostAsync(
	ctx context.Context, rc RequestContext, aspireEnv string, observer *Observer[ProgressMessage],
) (*AspireHost, error) {
	session, err := s.server.validateSession(rc.Session)
	if err != nil {
		return nil, err
	}

	var c struct {
		dotnetCli *dotnet.Cli `container:"type"`
	}

	container, err := session.newContainer(rc)
	if err != nil {
		return nil, err
	}

	if err := container.Fill(&c); err != nil {
		return nil, err
	}

	hostInfo := &AspireHost{
		Name: filepath.Base(filepath.Dir(rc.HostProjectPath)),
		Path: rc.HostProjectPath,
	}

	manifest, err := apphost.ManifestFromAppHost(ctx, rc.HostProjectPath, c.dotnetCli, aspireEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to load app host manifest: %w", err)
	}

	hostInfo.Services = servicesFromManifest(manifest)
	return hostInfo, nil
}

// RenameAspireHostAsync is the server implementation of:
// ValueTask RenameAspireHostAsync(Session session, string newPath, CancellationToken cancellationToken).
func (s *aspireService) RenameAspireHostAsync(
	ctx context.Context, rc RequestContext, newPath string, observer *Observer[ProgressMessage],
) error {
	_, err := s.server.validateSession(rc.Session)
	if err != nil {
		return err
	}

	// TODO(azure/azure-dev#3283): What should this do?  Rewrite azure.yaml?  We'll end up losing comments...
	return errors.New("not implemented")
}

// ServeHTTP implements http.Handler.
func (s *aspireService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveRpc(w, r, map[string]Handler{
		"GetAspireHostAsync":    NewHandler(s.GetAspireHostAsync),
		"RenameAspireHostAsync": NewHandler(s.RenameAspireHostAsync),
	})
}
