// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	osexec "os/exec"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/containerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
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

func mapContainerPublishError(err error) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*azcore.ResponseError](err); ok ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	remoteBuildErr, ok := errors.AsType[*containerregistry.RemoteBuildRunError](err)
	if !ok {
		return err
	}

	diagnosticCode := remoteBuildErr.DiagnosticCode()
	if diagnosticCode == "" {
		return err
	}

	return &azdext.LocalError{
		Message:  err.Error(),
		Code:     "container_publish_" + diagnosticCode,
		Category: azdext.LocalErrorCategoryInternal,
	}
}

func mapContainerToolError(err error) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*azcore.ResponseError](err); ok ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if _, ok := errors.AsType[*azdext.ServiceError](err); ok {
		return err
	}
	if _, ok := errors.AsType[*azdext.LocalError](err); ok {
		return err
	}
	if _, ok := errors.AsType[*azdext.ToolError](err); ok {
		return err
	}

	if missingErr, ok := errors.AsType[*tools.MissingToolErrors](err); ok {
		return &azdext.ToolError{
			Message:  err.Error(),
			Err:      err,
			ToolName: missingToolName(missingErr),
			Kind:     azdext.ToolErrorKindMissing,
		}
	}

	if exitErr, ok := errors.AsType[*azdexec.ExitError](err); ok {
		exitCode := exitErr.ExitCode
		return &azdext.ToolError{
			Message:  err.Error(),
			Err:      err,
			ToolName: normalizedToolName(exitErr.Cmd),
			Kind:     azdext.ToolErrorKindFailed,
			ExitCode: &exitCode,
		}
	}

	if processErr, ok := errors.AsType[*osexec.Error](err); ok {
		kind := azdext.ToolErrorKindFailed
		if errors.Is(processErr.Err, osexec.ErrNotFound) {
			kind = azdext.ToolErrorKindMissing
		}
		return &azdext.ToolError{
			Message:  err.Error(),
			Err:      err,
			ToolName: normalizedToolName(processErr.Name),
			Kind:     kind,
		}
	}

	return err
}

func normalizedToolName(name string) string {
	if normalized := azdexec.CommandName(name); normalized != "" {
		return normalized
	}

	return "other"
}

func missingToolName(err *tools.MissingToolErrors) string {
	if len(err.ToolNames) != 1 {
		return "multiple"
	}

	return tools.TelemetryName(err.ToolNames[0])
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

	serviceConfig, has := projectConfig.ServiceConfigs()[req.ServiceName]
	if !has {
		return nil, status.Errorf(codes.NotFound,
			"service %q not found in project configuration", req.ServiceName)
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
		return nil, mapContainerToolError(err)
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

	serviceConfig, has := projectConfig.ServiceConfigs()[req.ServiceName]
	if !has {
		return nil, status.Errorf(codes.NotFound,
			"service %q not found in project configuration", req.ServiceName)
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
		return nil, mapContainerToolError(err)
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

	serviceConfig, has := projectConfig.ServiceConfigs()[req.ServiceName]
	if !has {
		return nil, status.Errorf(codes.NotFound,
			"service %q not found in project configuration", req.ServiceName)
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
		return nil, mapContainerToolError(mapContainerPublishError(err))
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
