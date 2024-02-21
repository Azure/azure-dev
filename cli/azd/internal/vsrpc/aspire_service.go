// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
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
	ctx context.Context, sessionId Session, aspireEnv string, observer IObserver[ProgressMessage],
) (*AspireHost, error) {
	session, err := s.server.validateSession(ctx, sessionId)
	if err != nil {
		return nil, err
	}

	session.sessionMu.Lock()
	defer session.sessionMu.Unlock()

	var c struct {
		azdContext *azdcontext.AzdContext `container:"type"`
		dotnetCli  dotnet.DotNetCli       `container:"type"`
	}

	if err := session.container.Fill(&c); err != nil {
		return nil, err
	}

	// If there is an azure.yaml, load it and return the services.
	if _, err := os.Stat(c.azdContext.ProjectPath()); err == nil {
		projectConfig, err := project.Load(context.Background(), c.azdContext.ProjectPath())
		if err != nil {
			return nil, fmt.Errorf("loading %s: %w", c.azdContext.ProjectPath(), err)
		}

		if session.appHostPath == "" {
			appHost, err := appHostForProject(ctx, projectConfig, c.dotnetCli)
			if err != nil {
				return nil, err
			}

			session.appHostPath = appHost.Path()
		}

		hostInfo := &AspireHost{
			Name: filepath.Base(filepath.Dir(session.appHostPath)),
			Path: session.appHostPath,
		}

		manifest, err := session.readManifest(ctx, session.appHostPath, c.dotnetCli)
		if err != nil {
			return nil, fmt.Errorf("failed to load app host manifest: %w", err)
		}

		hostInfo.Services = servicesFromManifest(manifest)

		return hostInfo, nil

	} else if errors.Is(err, os.ErrNotExist) {
		hosts, err := appdetect.DetectAspireHosts(ctx, c.azdContext.ProjectDirectory(), c.dotnetCli)
		if err != nil {
			return nil, fmt.Errorf("failed to discover app host project under %s: %w", c.azdContext.ProjectPath(), err)
		}

		if len(hosts) == 0 {
			return nil, fmt.Errorf("no app host projects found under %s", c.azdContext.ProjectPath())
		}

		if len(hosts) > 1 {
			return nil, fmt.Errorf("multiple app host projects found under %s", c.azdContext.ProjectPath())
		}

		hostInfo := &AspireHost{
			Name: filepath.Base(filepath.Dir(session.appHostPath)),
			Path: hosts[0].Path,
		}

		session.appHostPath = hostInfo.Path

		manifest, err := session.readManifest(ctx, session.appHostPath, c.dotnetCli)
		if err != nil {
			return nil, fmt.Errorf("failed to load app host manifest: %w", err)
		}

		hostInfo.Services = servicesFromManifest(manifest)

		return hostInfo, nil

	} else {
		return nil, fmt.Errorf("failed to stat project path: %w", err)
	}

}

// RenameAspireHostAsync is the server implementation of:
// ValueTask RenameAspireHostAsync(Session session, string newPath, CancellationToken cancellationToken).
func (s *aspireService) RenameAspireHostAsync(
	ctx context.Context, sessionId Session, newPath string, observer IObserver[ProgressMessage],
) error {
	session, err := s.server.validateSession(ctx, sessionId)
	if err != nil {
		return err
	}

	session.sessionMu.Lock()
	defer session.sessionMu.Unlock()

	// TODO(azure/azure-dev#3283): What should this do?  Rewrite azure.yaml?  We'll end up losing comments...
	return errors.New("not implemented")
}

// ServeHTTP implements http.Handler.
func (s *aspireService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveRpc(w, r, map[string]Handler{
		"GetAspireHostAsync":    HandlerFunc3(s.GetAspireHostAsync),
		"RenameAspireHostAsync": HandlerAction3(s.RenameAspireHostAsync),
	})
}
