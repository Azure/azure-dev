// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

// OpenEnvironmentAsync is the server implementation of:
// ValueTask<Environment> OpenEnvironmentAsync(RequestContext, string, IObserver<ProgressMessage>, CancellationToken);
//
// OpenEnvironmentAsync loads the specified environment, without connecting to Azure or fetching a manifest (unless it is
// already cached) and is faster than `LoadEnvironmentAsync` in cases where we have not cached the manifest. This means
// the Services array of the returned environment may be empty.
func (s *environmentService) OpenEnvironmentAsync(
	ctx context.Context, rc RequestContext, name string, observer *Observer[ProgressMessage],
) (*Environment, error) {
	session, err := s.server.validateSession(rc.Session)
	if err != nil {
		return nil, err
	}

	container, err := session.newContainer(rc)
	if err != nil {
		return nil, err
	}

	return s.loadEnvironmentAsync(ctx, container, name, false)
}

// LoadEnvironmentAsync is the server implementation of:
// ValueTask<Environment> LoadEnvironmentAsync(RequestContext, string, IObserver<ProgressMessage>, CancellationToken);
//
// LoadEnvironmentAsync loads the specified environment, without connecting to Azure. Because of this, certain properties of
// the environment (like service endpoints) may not be available. Use `RefreshEnvironmentAsync` to load the environment and
// fetch information from Azure.
func (s *environmentService) LoadEnvironmentAsync(
	ctx context.Context, rc RequestContext, name string, observer *Observer[ProgressMessage],
) (*Environment, error) {
	session, err := s.server.validateSession(rc.Session)
	if err != nil {
		return nil, err
	}

	container, err := session.newContainer(rc)
	if err != nil {
		return nil, err
	}

	return s.loadEnvironmentAsync(ctx, container, name, true)
}

func (s *environmentService) loadEnvironmentAsync(
	ctx context.Context, container *container, name string, mustLoadServices bool,
) (*Environment, error) {
	var c struct {
		azdCtx         *azdcontext.AzdContext  `container:"type"`
		envManager     environment.Manager     `container:"type"`
		projectConfig  *project.ProjectConfig  `container:"type"`
		dotnetCli      *dotnet.Cli             `container:"type"`
		dotnetImporter *project.DotNetImporter `container:"type"`
	}

	if err := container.Fill(&c); err != nil {
		return nil, err
	}

	e, err := c.envManager.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("getting environment: %w", err)
	}

	currentEnv, err := c.azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, fmt.Errorf("getting default environment: %w", err)
	}

	ret := &Environment{
		Name: name,
		Properties: map[string]string{
			"Subscription": e.GetSubscriptionId(),
			"Location":     e.GetLocation(),
		},
		IsCurrent: name == currentEnv,
		Values:    e.Dotenv(),
	}

	// NOTE(ellismg): The IaC for Aspire Apps exposes these properties - we use them instead of trying to discover the
	// deployed resources (perhaps by considering the resources in the resource group associated with the environment or
	// by looking at the deployment).  This was the quickest path to get the information that VS needed for the spike,
	// but we might want to revisit this strategy. A nice thing about this strategy is it means we can return the data
	// promptly, which is nice for VS.
	if v := e.Getenv("AZURE_CONTAINER_APPS_ENVIRONMENT_ID"); v != "" {
		parts := strings.Split(v, "/")
		ret.Properties["ContainerAppsEnvironment"] = parts[len(parts)-1]
	}

	if v := e.Getenv("AZURE_CONTAINER_REGISTRY_ENDPOINT"); v != "" {
		ret.Properties["ContainerRegistry"] = strings.TrimSuffix(v, ".azurecr.io")
	}

	if v := e.Getenv("AZURE_LOG_ANALYTICS_WORKSPACE_NAME"); v != "" {
		ret.Properties["LogAnalyticsWorkspace"] = v
	}

	// If we would have to discover the app host or load the manifest from disk and the caller did not request it
	// skip this somewhat expensive operation, at the expense of not building out the services array.
	if !mustLoadServices {
		return ret, nil
	}

	appHost, err := appHostForProject(ctx, c.projectConfig, c.dotnetCli)
	if err != nil {
		return nil, fmt.Errorf("failed to find Aspire app host: %w", err)
	}

	manifest, err := c.dotnetImporter.ReadManifest(ctx, appHost)
	if err != nil {
		return nil, fmt.Errorf("reading app host manifest: %w", err)
	}

	ret.Services = servicesFromManifest(manifest)

	return ret, nil
}
