package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type containerService struct {
	azdext.UnimplementedContainerServiceServer
	lazyContainerHelper *lazy.Lazy[*project.ContainerHelper]
	lazyResourceManager *lazy.Lazy[project.ResourceManager]
	lazyProject         *lazy.Lazy[*project.ProjectConfig]
	lazyEnvironment     *lazy.Lazy[*environment.Environment]
}

func NewContainerService(
	lazyContainerHelper *lazy.Lazy[*project.ContainerHelper],
	lazyResourceManager *lazy.Lazy[project.ResourceManager],
	lazyProjectConf *lazy.Lazy[*project.ProjectConfig],
	lazyEnvironment *lazy.Lazy[*environment.Environment],
) azdext.ContainerServiceServer {
	return &containerService{
		lazyContainerHelper: lazyContainerHelper,
		lazyResourceManager: lazyResourceManager,
		lazyProject:         lazyProjectConf,
		lazyEnvironment:     lazyEnvironment,
	}
}

// Package implements azdext.ContainerServiceServer.
func (c *containerService) Package(ctx context.Context, req *azdext.ContainerPackageRequest) (*azdext.ContainerPackageResponse, error) {
	projectConfig, err := c.lazyProject.GetValue()
	if err != nil {
		return nil, err
	}

	serviceConfig, has := projectConfig.Services[req.ServiceName]
	if !has {
		return nil, fmt.Errorf("service %q not found in project configuration", req.ServiceName)
	}

	containerHelper, err := c.lazyContainerHelper.GetValue()
	if err != nil {
		return nil, err
	}

	// Initialize service context with build artifacts if any
	serviceContext := &project.ServiceContext{}
	// Build artifacts would typically be provided by a prior build step
	// For now, initialize empty context

	progress := async.NewProgress[project.ServiceProgress]()

	packageResult, err := containerHelper.Package(ctx, serviceConfig, serviceContext, progress)
	if err != nil {
		return nil, err
	}

	// Use mapper to convert ServicePackageResult to proto
	var protoResult *azdext.ServicePackageResult
	if err := mapper.Convert(packageResult, &protoResult); err != nil {
		return nil, fmt.Errorf("failed to convert package result: %w", err)
	}

	return &azdext.ContainerPackageResponse{
		Result: protoResult,
	}, nil
}

// Publish implements azdext.ContainerServiceServer.
func (c *containerService) Publish(ctx context.Context, req *azdext.ContainerPublishRequest) (*azdext.ContainerPublishResponse, error) {
	projectConfig, err := c.lazyProject.GetValue()
	if err != nil {
		return nil, err
	}

	serviceConfig, has := projectConfig.Services[req.ServiceName]
	if !has {
		return nil, fmt.Errorf("service %q not found in project configuration", req.ServiceName)
	}

	env, err := c.lazyEnvironment.GetValue()
	if err != nil {
		return nil, err
	}

	resourceManager, err := c.lazyResourceManager.GetValue()
	if err != nil {
		return nil, err
	}

	containerHelper, err := c.lazyContainerHelper.GetValue()
	if err != nil {
		return nil, err
	}

	targetResource, err := resourceManager.GetTargetResource(ctx, env.GetSubscriptionId(), serviceConfig)
	if err != nil {
		return nil, err
	}

	progress := async.NewProgress[project.ServiceProgress]()

	serviceContext := &project.ServiceContext{}
	if err := mapper.Convert(req.ServiceContext, &serviceContext); err != nil {
		return nil, err
	}

	publishResult, err := containerHelper.Publish(
		ctx,
		serviceConfig,
		serviceContext,
		targetResource,
		progress,
		nil,
	)
	if err != nil {
		return nil, err
	}

	// Use mapper to convert ServicePublishResult to proto
	var protoResult *azdext.ServicePublishResult
	if err := mapper.Convert(publishResult, &protoResult); err != nil {
		return nil, fmt.Errorf("failed to convert publish result: %w", err)
	}

	return &azdext.ContainerPublishResponse{
		Result: protoResult,
	}, nil
}
