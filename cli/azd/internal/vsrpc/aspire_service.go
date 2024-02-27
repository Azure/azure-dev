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
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
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

	var c struct {
		azdContext *azdcontext.AzdContext `container:"type"`
		dotnetCli  dotnet.DotNetCli       `container:"type"`
	}

	container, err := session.newContainer()
	if err != nil {
		return nil, err
	}

	if err := container.Fill(&c); err != nil {
		return nil, err
	}

	// If there is an azure.yaml, load it and return the services.
	if _, err := os.Stat(c.azdContext.ProjectPath()); err == nil {
		var cc struct {
			projectConfig *project.ProjectConfig `container:"type"`
		}

		if err := container.Fill(&cc); err != nil {
			return nil, err
		}

		appHost, err := appHostForProject(ctx, cc.projectConfig, c.dotnetCli)
		if err != nil {
			return nil, err
		}

		hostInfo := &AspireHost{
			Name: filepath.Base(filepath.Dir(appHost.Path())),
			Path: appHost.Path(),
		}

		manifest, err := apphost.ManifestFromAppHost(ctx, appHost.Path(), c.dotnetCli, aspireEnv)
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
			Name: filepath.Base(filepath.Dir(hosts[0].Path)),
			Path: hosts[0].Path,
		}

		manifest, err := apphost.ManifestFromAppHost(ctx, hosts[0].Path, c.dotnetCli, aspireEnv)
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
	_, err := s.server.validateSession(ctx, sessionId)
	if err != nil {
		return err
	}

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
