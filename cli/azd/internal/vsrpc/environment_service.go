// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
)

// environmentService is the RPC server for the '/EnvironmentService/v1.0' endpoint.
type environmentService struct {
	server *Server
}

func newEnvironmentService(server *Server) *environmentService {
	return &environmentService{
		server: server,
	}
}

// GetEnvironmentsAsync is the server implementation of:
// ValueTask<IEnumerable<EnvironmentInfo>> GetEnvironmentsAsync(Session, IObserver<ProgressMessage>, CancellationToken);
func (s *environmentService) GetEnvironmentsAsync(
	ctx context.Context, sessionId Session, observer IObserver[ProgressMessage],
) ([]*EnvironmentInfo, error) {
	session, err := s.server.validateSession(ctx, sessionId)
	if err != nil {
		return nil, err
	}

	var c struct {
		envManager environment.Manager `container:"type"`
	}

	container, err := session.newContainer()
	if err != nil {
		return nil, err
	}
	if err := container.Fill(&c); err != nil {
		return nil, err
	}

	envs, err := c.envManager.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing environments: %w", err)
	}

	infos := make([]*EnvironmentInfo, len(envs))
	for i, env := range envs {
		infos[i] = &EnvironmentInfo{
			Name:       env.Name,
			IsCurrent:  env.IsDefault,
			DotEnvPath: env.DotEnvPath,
		}
	}

	return infos, nil
}

// SetCurrentEnvironmentAsync is the server implementation of:
// ValueTask<bool> SetCurrentEnvironmentAsync(Session, string, IObserver<ProgressMessage>, CancellationToken);
func (s *environmentService) SetCurrentEnvironmentAsync(
	ctx context.Context, sessionId Session, name string, observer IObserver[ProgressMessage],
) (bool, error) {
	session, err := s.server.validateSession(ctx, sessionId)
	if err != nil {
		return false, err
	}

	var c struct {
		azdCtx *azdcontext.AzdContext `container:"type"`
	}

	container, err := session.newContainer()
	if err != nil {
		return false, err
	}
	if err := container.Fill(&c); err != nil {
		return false, err
	}

	if err := c.azdCtx.SetDefaultEnvironmentName(name); err != nil {
		return false, fmt.Errorf("saving default environment: %w", err)
	}

	return true, nil
}

// DeleteEnvironmentAsync is the server implementation of:
// ValueTask<bool> DeleteEnvironmentAsync(Session, string, IObserver<ProgressMessage>, CancellationToken);
func (s *environmentService) DeleteEnvironmentAsync(
	ctx context.Context, sessionId Session, name string, observer IObserver[ProgressMessage],
) (bool, error) {
	_, err := s.server.validateSession(ctx, sessionId)
	if err != nil {
		return false, err
	}

	// TODO(azure/azure-dev#3285): Implement this.
	return false, errors.New("not implemented")
}

// ServeHTTP implements http.Handler.
func (s *environmentService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveRpc(w, r, map[string]Handler{
		"CreateEnvironmentAsync":     HandlerFunc3(s.CreateEnvironmentAsync),
		"GetEnvironmentsAsync":       HandlerFunc2(s.GetEnvironmentsAsync),
		"LoadEnvironmentAsync":       HandlerFunc3(s.LoadEnvironmentAsync),
		"OpenEnvironmentAsync":       HandlerFunc3(s.OpenEnvironmentAsync),
		"SetCurrentEnvironmentAsync": HandlerFunc3(s.SetCurrentEnvironmentAsync),
		"DeleteEnvironmentAsync":     HandlerFunc3(s.DeleteEnvironmentAsync),
		"RefreshEnvironmentAsync":    HandlerFunc3(s.RefreshEnvironmentAsync),
		"DeployAsync":                HandlerFunc3(s.DeployAsync),
	})
}
