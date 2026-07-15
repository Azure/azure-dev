// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	if req.ServiceName == "" {
		return nil, status.Error(codes.InvalidArgument, "service name is required")
	}

	projectConfig, err := c.lazyProject.GetValue()
	if err != nil {
		return nil, err
	}

	serviceConfig, err := containerServiceConfig(
		projectConfig,
		req.ServiceName,
		req.ServicePath,
	)
	if err != nil {
		return nil, err
	}

	containerHelper, err := c.lazyContainerHelper.GetValue()
	if err != nil {
		return nil, err
	}

	var serviceContext *project.ServiceContext
	if err := mapper.Convert(req.ServiceContext, &serviceContext); err != nil {
		return nil, err
	}

	env, err := c.lazyEnvironment.GetValue()
	if err != nil {
		return nil, err
	}

	// Call containerHelper.Build with noop progress reporting to avoid conflicts with outer progress layer
	progress := async.NewNoopProgress[project.ServiceProgress]()
	defer progress.Done()

	buildResult, err := containerHelper.Build(ctx, serviceConfig, serviceContext, env, progress)
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
	if req.ServiceName == "" {
		return nil, status.Error(codes.InvalidArgument, "service name is required")
	}

	projectConfig, err := c.lazyProject.GetValue()
	if err != nil {
		return nil, err
	}

	serviceConfig, err := containerServiceConfig(
		projectConfig,
		req.ServiceName,
		req.ServicePath,
	)
	if err != nil {
		return nil, err
	}

	containerHelper, err := c.lazyContainerHelper.GetValue()
	if err != nil {
		return nil, err
	}

	var serviceContext *project.ServiceContext
	if err := mapper.Convert(req.ServiceContext, &serviceContext); err != nil {
		return nil, err
	}

	env, err := c.lazyEnvironment.GetValue()
	if err != nil {
		return nil, err
	}

	// Call containerHelper.Package with noop progress reporting to avoid conflicts with outer progress layer
	progress := async.NewNoopProgress[project.ServiceProgress]()
	defer progress.Done()

	packageResult, err := containerHelper.Package(ctx, serviceConfig, serviceContext, env, progress)
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
	if req.ServiceName == "" {
		return nil, status.Error(codes.InvalidArgument, "service name is required")
	}

	projectConfig, err := c.lazyProject.GetValue()
	if err != nil {
		return nil, err
	}

	serviceConfig, err := containerServiceConfig(
		projectConfig,
		req.ServiceName,
		req.ServicePath,
	)
	if err != nil {
		return nil, err
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

	env, err := c.lazyEnvironment.GetValue()
	if err != nil {
		return nil, err
	}

	// Call containerHelper.Publish with noop progress reporting to avoid conflicts with outer progress layer
	progress := async.NewNoopProgress[project.ServiceProgress]()
	defer progress.Done()

	publishResult, err := containerHelper.Publish(ctx, serviceConfig, serviceContext, targetResource, env, progress, nil)
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

func containerServiceConfig(
	projectConfig *project.ProjectConfig,
	serviceName string,
	servicePath string,
) (*project.ServiceConfig, error) {
	serviceConfig, has := projectConfig.Services[serviceName]
	if !has {
		return nil, status.Errorf(
			codes.NotFound,
			"service %q not found in project configuration",
			serviceName,
		)
	}
	if servicePath == "" {
		return serviceConfig, nil
	}
	if err := validateContainerServicePath(
		projectConfig.Path,
		servicePath,
	); err != nil {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"service path %q must stay within the project: %v",
			servicePath,
			err,
		)
	}
	effective := *serviceConfig
	effective.RelativePath = servicePath
	return &effective, nil
}

func validateContainerServicePath(
	projectRoot string,
	servicePath string,
) error {
	if !filepath.IsLocal(servicePath) {
		return errors.New("path must be project-relative")
	}
	if strings.TrimSpace(projectRoot) == "" {
		return errors.New("project path is empty")
	}
	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve project path symlinks: %w", err)
	}

	targetReal, err := resolvedExistingAncestor(
		filepath.Join(rootAbs, servicePath),
	)
	if err != nil {
		return fmt.Errorf("resolve service path symlinks: %w", err)
	}
	relative, err := filepath.Rel(rootReal, targetReal)
	if err != nil {
		return fmt.Errorf("compare service path to project: %w", err)
	}
	if relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("resolved path escapes project")
	}
	return nil
}

func resolvedExistingAncestor(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			return filepath.Clean(resolved), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New(
				"service path has no existing ancestor",
			)
		}
		current = parent
	}
}
