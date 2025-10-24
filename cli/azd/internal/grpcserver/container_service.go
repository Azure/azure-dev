// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type containerService struct {
	azdext.UnimplementedContainerServiceServer
	console             input.Console
	lazyContainerHelper *lazy.Lazy[*project.ContainerHelper]
	lazyServiceManager  *lazy.Lazy[project.ServiceManager]
	lazyProject         *lazy.Lazy[*project.ProjectConfig]
	lazyEnvironment     *lazy.Lazy[*environment.Environment]
}

func NewContainerService(
	console input.Console,
	lazyContainerHelper *lazy.Lazy[*project.ContainerHelper],
	lazyServiceManager *lazy.Lazy[project.ServiceManager],
	lazyProjectConf *lazy.Lazy[*project.ProjectConfig],
	lazyEnvironment *lazy.Lazy[*environment.Environment],
) azdext.ContainerServiceServer {
	return &containerService{
		console:             console,
		lazyContainerHelper: lazyContainerHelper,
		lazyServiceManager:  lazyServiceManager,
		lazyProject:         lazyProjectConf,
		lazyEnvironment:     lazyEnvironment,
	}
}

func (c *containerService) Build(
	ctx context.Context,
	req *azdext.ContainerBuildRequest,
) (*azdext.ContainerBuildResponse, error) {
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

	var serviceContext *project.ServiceContext
	if err := mapper.Convert(req.ServiceContext, &serviceContext); err != nil {
		return nil, err
	}

	buildResult, err := async.RunWithProgress(
		func(buildProgress project.ServiceProgress) {
			progressMessage := fmt.Sprintf("Building service %s (%s)", serviceConfig.Name, buildProgress.Message)
			c.console.ShowSpinner(ctx, progressMessage, input.Step)
		},
		func(progress *async.Progress[project.ServiceProgress]) (*project.ServiceBuildResult, error) {
			return containerHelper.Build(ctx, serviceConfig, serviceContext, progress)
		},
	)
	if err != nil {
		return nil, err
	}

	// Use mapper to convert ServiceBuildResult to proto
	var protoResult *azdext.ServiceBuildResult
	if err := mapper.Convert(buildResult, &protoResult); err != nil {
		return nil, fmt.Errorf("failed to convert build result: %w", err)
	}

	return &azdext.ContainerBuildResponse{
		Result: protoResult,
	}, nil
}

// Package implements azdext.ContainerServiceServer.
func (c *containerService) Package(
	ctx context.Context,
	req *azdext.ContainerPackageRequest,
) (*azdext.ContainerPackageResponse, error) {
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

	var serviceContext *project.ServiceContext
	if err := mapper.Convert(req.ServiceContext, &serviceContext); err != nil {
		return nil, err
	}

	packageResult, err := async.RunWithProgress(
		func(buildProgress project.ServiceProgress) {
			progressMessage := fmt.Sprintf("Packaging service %s (%s)", serviceConfig.Name, buildProgress.Message)
			c.console.ShowSpinner(ctx, progressMessage, input.Step)
		},
		func(progress *async.Progress[project.ServiceProgress]) (*project.ServicePackageResult, error) {
			return containerHelper.Package(ctx, serviceConfig, serviceContext, progress)
		},
	)
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
func (c *containerService) Publish(
	ctx context.Context,
	req *azdext.ContainerPublishRequest,
) (*azdext.ContainerPublishResponse, error) {
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

	serviceManager, err := c.lazyServiceManager.GetValue()
	if err != nil {
		return nil, err
	}

	serviceTarget, err := serviceManager.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}

	targetResource, err := serviceManager.GetTargetResource(ctx, serviceConfig, serviceTarget)
	if err != nil {
		return nil, err
	}

	var serviceContext *project.ServiceContext
	if err := mapper.Convert(req.ServiceContext, &serviceContext); err != nil {
		return nil, err
	}

	publishResult, err := async.RunWithProgress(
		func(buildProgress project.ServiceProgress) {
			progressMessage := fmt.Sprintf("Publishing service %s (%s)", serviceConfig.Name, buildProgress.Message)
			c.console.ShowSpinner(ctx, progressMessage, input.Step)
		},
		func(progress *async.Progress[project.ServiceProgress]) (*project.ServicePublishResult, error) {
			return containerHelper.Publish(ctx, serviceConfig, serviceContext, targetResource, progress, nil)
		},
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
