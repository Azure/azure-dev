// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
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
// ValueTask<IEnumerable<EnvironmentInfo>> GetEnvironmentsAsync(
// RequestContext, IObserver<ProgressMessage>, CancellationToken);
func (s *environmentService) GetEnvironmentsAsync(
	ctx context.Context, rc RequestContext, observer IObserver[ProgressMessage],
) ([]*EnvironmentInfo, error) {
	session, err := s.server.validateSession(ctx, rc.Session)
	if err != nil {
		return nil, err
	}

	var c struct {
		envManager environment.Manager `container:"type"`
	}

	container, err := session.newContainer(rc)
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
// ValueTask<bool> SetCurrentEnvironmentAsync(RequestContext, string, IObserver<ProgressMessage>, CancellationToken);
func (s *environmentService) SetCurrentEnvironmentAsync(
	ctx context.Context, rc RequestContext, name string, observer IObserver[ProgressMessage],
) (bool, error) {
	session, err := s.server.validateSession(ctx, rc.Session)
	if err != nil {
		return false, err
	}

	var c struct {
		azdCtx *azdcontext.AzdContext `container:"type"`
	}

	container, err := session.newContainer(rc)
	if err != nil {
		return false, err
	}
	if err := container.Fill(&c); err != nil {
		return false, err
	}

	if err := c.azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: name}); err != nil {
		return false, fmt.Errorf("saving default environment: %w", err)
	}

	return true, nil
}

type DeleteMode uint32

const (
	DeleteModeLocal = 1 << iota
	DeleteModeAzureResources
)

// DeleteEnvironmentAsync is the server implementation of:
// ValueTask<bool> DeleteEnvironmentAsync(RequestContext, string, IObserver<ProgressMessage>, int, CancellationToken);
func (s *environmentService) DeleteEnvironmentAsync(
	ctx context.Context, rc RequestContext, name string, mode int, observer IObserver[ProgressMessage],
) (bool, error) {
	session, err := s.server.validateSession(ctx, rc.Session)
	if err != nil {
		return false, err
	}
	outputWriter := &lineWriter{
		next: &messageWriter{
			ctx:      ctx,
			observer: observer,
			messageTemplate: ProgressMessage{
				Kind:     MessageKind(Info),
				Severity: Info,
			},
		},
	}

	spinnerWriter := &lineWriter{
		trimLineEndings: true,
		next: &messageWriter{
			ctx:      ctx,
			observer: observer,
			messageTemplate: ProgressMessage{
				Kind:     MessageKind(Important),
				Severity: Info,
			},
		},
	}

	container, err := session.newContainer(rc)
	if err != nil {
		return false, err
	}
	container.outWriter.AddWriter(outputWriter)
	container.spinnerWriter.AddWriter(spinnerWriter)

	var c struct {
		provisionManager *provisioning.Manager  `container:"type"`
		envManager       environment.Manager    `container:"type"`
		importManager    *project.ImportManager `container:"type"`
		projectConfig    *project.ProjectConfig `container:"type"`
	}
	container.MustRegisterScoped(func() internal.EnvFlag {
		return internal.EnvFlag{
			EnvironmentName: name,
		}
	})

	if err := container.Fill(&c); err != nil {
		return false, err
	}

	if mode&DeleteModeAzureResources > 0 {
		_ = observer.OnNext(ctx, newImportantProgressMessage("Removing Azure resources"))

		infra, err := c.importManager.ProjectInfrastructure(ctx, c.projectConfig)
		if err != nil {
			return false, err
		}
		defer func() { _ = infra.Cleanup() }()

		if err := c.provisionManager.Initialize(ctx, c.projectConfig.Path, infra.Options); err != nil {
			return false, fmt.Errorf("initializing provisioning manager: %w", err)
		}

		// Enable force and purge options
		destroyOptions := provisioning.NewDestroyOptions(true, true)
		_, err = c.provisionManager.Destroy(ctx, destroyOptions)
		if errors.Is(err, provisioning.ErrDeploymentsNotFound) {
			_ = observer.OnNext(ctx, newInfoProgressMessage("No Azure resources were found"))
		} else if err != nil {
			return false, fmt.Errorf("deleting infrastructure: %w", err)
		}
	}

	if mode&DeleteModeLocal > 0 {
		_ = observer.OnNext(ctx, newImportantProgressMessage("Removing environment"))
		err = c.envManager.Delete(ctx, name)
		if err != nil {
			return false, err
		}
	}

	return true, nil
}

// ServeHTTP implements http.Handler.
func (s *environmentService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveRpc(w, r, map[string]Handler{
		"CreateEnvironmentAsync":     HandlerFunc3(s.CreateEnvironmentAsync),
		"GetEnvironmentsAsync":       HandlerFunc2(s.GetEnvironmentsAsync),
		"LoadEnvironmentAsync":       HandlerFunc3(s.LoadEnvironmentAsync),
		"OpenEnvironmentAsync":       HandlerFunc3(s.OpenEnvironmentAsync),
		"SetCurrentEnvironmentAsync": HandlerFunc3(s.SetCurrentEnvironmentAsync),
		"DeleteEnvironmentAsync":     HandlerFunc4(s.DeleteEnvironmentAsync),
		"RefreshEnvironmentAsync":    HandlerFunc3(s.RefreshEnvironmentAsync),
		"DeployAsync":                HandlerFunc3(s.DeployAsync),
	})
}
