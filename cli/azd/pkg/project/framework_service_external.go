// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/google/uuid"
)

// externalTool implements the tools.ExternalTool interface for extension-provided tools
type externalTool struct {
	name       string
	installUrl string
}

func (et *externalTool) CheckInstalled(ctx context.Context) error {
	// Extension-provided tools are assumed to be checked by the extension itself
	return nil
}

func (et *externalTool) InstallUrl() string {
	return et.installUrl
}

func (et *externalTool) Name() string {
	return et.name
}

type ExternalFrameworkService struct {
	extension    *extensions.Extension
	languageName string
	languageKind ServiceLanguageKind
	console      input.Console

	broker *grpcbroker.MessageBroker[azdext.FrameworkServiceMessage]
}

// NewExternalFrameworkService creates a new external framework service
func NewExternalFrameworkService(
	name string,
	kind ServiceLanguageKind,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[azdext.FrameworkServiceMessage],
	console input.Console,
) FrameworkService {
	service := &ExternalFrameworkService{
		extension:    extension,
		languageName: name,
		languageKind: kind,
		console:      console,
		broker:       broker,
	}

	return service
}

// RequiredExternalTools gets a list of the required external tools for the framework service
func (efs *ExternalFrameworkService) RequiredExternalTools(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) []tools.ExternalTool {
	// Convert serviceConfig to gRPC proto
	protoServiceConfig, err := efs.toProtoServiceConfig(serviceConfig)
	if err != nil {
		return nil
	}

	req := &azdext.FrameworkServiceMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.FrameworkServiceMessage_RequiredExternalToolsRequest{
			RequiredExternalToolsRequest: &azdext.FrameworkServiceRequiredExternalToolsRequest{
				ServiceConfig: protoServiceConfig,
			},
		},
	}

	resp, err := efs.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil
	}

	toolsResp := resp.GetRequiredExternalToolsResponse()
	if toolsResp == nil {
		return nil
	}

	var externalTools []tools.ExternalTool
	for _, protoTool := range toolsResp.Tools {
		// Create a simple implementation of ExternalTool interface
		tool := &externalTool{
			name:       protoTool.Name,
			installUrl: protoTool.InstallUrl,
		}
		externalTools = append(externalTools, tool)
	}

	return externalTools
}

// Initialize initializes the framework service for the specified service configuration
func (efs *ExternalFrameworkService) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	if serviceConfig == nil {
		return errors.New("service configuration is required")
	}

	protoServiceConfig, err := efs.toProtoServiceConfig(serviceConfig)
	if err != nil {
		return err
	}

	req := &azdext.FrameworkServiceMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.FrameworkServiceMessage_InitializeRequest{
			InitializeRequest: &azdext.FrameworkServiceInitializeRequest{
				ServiceConfig: protoServiceConfig,
			},
		},
	}

	_, err = efs.broker.SendAndWait(ctx, req)
	return err
}

// Requirements gets the requirements for the language or framework service
func (efs *ExternalFrameworkService) Requirements() FrameworkRequirements {
	ctx := context.Background()
	req := &azdext.FrameworkServiceMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.FrameworkServiceMessage_RequirementsRequest{
			RequirementsRequest: &azdext.FrameworkServiceRequirementsRequest{
				// Empty - requirements are static for a framework
			},
		},
	}

	resp, err := efs.broker.SendAndWait(ctx, req)
	if err != nil {
		// Return default requirements on error
		return FrameworkRequirements{
			Package: FrameworkPackageRequirements{
				RequireRestore: false,
				RequireBuild:   false,
			},
		}
	}

	reqResp := resp.GetRequirementsResponse()
	if reqResp == nil || reqResp.Requirements == nil {
		return FrameworkRequirements{
			Package: FrameworkPackageRequirements{
				RequireRestore: false,
				RequireBuild:   false,
			},
		}
	}

	protoReqs := reqResp.Requirements
	requirements := FrameworkRequirements{}

	if protoReqs.Package != nil {
		requirements.Package = FrameworkPackageRequirements{
			RequireRestore: protoReqs.Package.RequireRestore,
			RequireBuild:   protoReqs.Package.RequireBuild,
		}
	}

	return requirements
}

// Restore restores dependencies for the framework service
func (efs *ExternalFrameworkService) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	protoServiceConfig, err := efs.toProtoServiceConfig(serviceConfig)
	if err != nil {
		return nil, err
	}

	req := &azdext.FrameworkServiceMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.FrameworkServiceMessage_RestoreRequest{
			RestoreRequest: &azdext.FrameworkServiceRestoreRequest{
				ServiceConfig: protoServiceConfig,
			},
		},
	}

	resp, err := efs.broker.SendAndWaitWithProgress(ctx, req, createProgressFunc(progress))
	if err != nil {
		return nil, err
	}

	restoreResp := resp.GetRestoreResponse()
	if restoreResp == nil {
		return nil, fmt.Errorf("received empty restore response")
	}

	var result *ServiceRestoreResult
	err = mapper.Convert(restoreResp.RestoreResult, &result)
	if err != nil {
		return nil, fmt.Errorf("converting restore result: %w", err)
	}

	return result, nil
}

// Build builds the source for the framework service
func (efs *ExternalFrameworkService) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	protoServiceConfig := &azdext.ServiceConfig{}
	if err := mapper.Convert(serviceConfig, &protoServiceConfig); err != nil {
		return nil, err
	}

	protoServiceContext := &azdext.ServiceContext{}
	if err := mapper.Convert(serviceContext, &protoServiceContext); err != nil {
		return nil, err
	}

	req := &azdext.FrameworkServiceMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.FrameworkServiceMessage_BuildRequest{
			BuildRequest: &azdext.FrameworkServiceBuildRequest{
				ServiceConfig:  protoServiceConfig,
				ServiceContext: protoServiceContext,
			},
		},
	}

	resp, err := efs.broker.SendAndWaitWithProgress(ctx, req, createProgressFunc(progress))
	if err != nil {
		return nil, err
	}

	buildResp := resp.GetBuildResponse()
	if buildResp == nil {
		return nil, fmt.Errorf("received empty build response")
	}

	var result *ServiceBuildResult
	err = mapper.Convert(buildResp.Result, &result)
	if err != nil {
		return nil, fmt.Errorf("converting build result: %w", err)
	}

	return result, nil
}

// Package packages the source suitable for deployment
func (efs *ExternalFrameworkService) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	protoServiceConfig := &azdext.ServiceConfig{}
	if err := mapper.Convert(serviceConfig, &protoServiceConfig); err != nil {
		return nil, err
	}

	protoServiceContext := &azdext.ServiceContext{}
	if err := mapper.Convert(serviceContext, &protoServiceContext); err != nil {
		return nil, err
	}

	req := &azdext.FrameworkServiceMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.FrameworkServiceMessage_PackageRequest{
			PackageRequest: &azdext.FrameworkServicePackageRequest{
				ServiceConfig:  protoServiceConfig,
				ServiceContext: protoServiceContext,
			},
		},
	}

	resp, err := efs.broker.SendAndWaitWithProgress(ctx, req, createProgressFunc(progress))
	if err != nil {
		return nil, err
	}

	packageResp := resp.GetPackageResponse()
	if packageResp == nil {
		return nil, fmt.Errorf("received empty package response")
	}

	var result *ServicePackageResult
	err = mapper.Convert(packageResp.PackageResult, &result)
	if err != nil {
		return nil, fmt.Errorf("converting package result: %w", err)
	}

	return result, nil
}

// Convert ServiceConfig to proto message
func (efs *ExternalFrameworkService) toProtoServiceConfig(serviceConfig *ServiceConfig) (*azdext.ServiceConfig, error) {
	if serviceConfig == nil {
		return nil, nil
	}

	// Use an empty resolver since ExternalFrameworkService doesn't have access to environment
	// The extension is responsible for handling environment variable substitution
	emptyResolver := func(key string) string {
		return ""
	}

	var protoConfig *azdext.ServiceConfig
	err := mapper.WithResolver(emptyResolver).Convert(serviceConfig, &protoConfig)
	if err != nil {
		return nil, fmt.Errorf("converting service config: %w", err)
	}

	return protoConfig, nil
}
