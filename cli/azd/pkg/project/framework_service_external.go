// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
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

	stream        azdext.FrameworkService_StreamServer
	responseChans sync.Map
}

// NewExternalFrameworkService creates a new external framework service
func NewExternalFrameworkService(
	name string,
	kind ServiceLanguageKind,
	extension *extensions.Extension,
	stream azdext.FrameworkService_StreamServer,
	console input.Console,
) FrameworkService {
	service := &ExternalFrameworkService{
		extension:    extension,
		languageName: name,
		languageKind: kind,
		console:      console,
		stream:       stream,
	}

	service.startResponseDispatcher()

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

	resp, err := efs.sendAndWait(ctx, req, func(r *azdext.FrameworkServiceMessage) bool {
		return r.GetRequiredExternalToolsResponse() != nil
	})
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

	_, err = efs.sendAndWait(ctx, req, func(r *azdext.FrameworkServiceMessage) bool {
		return r.GetInitializeResponse() != nil
	})
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

	resp, err := efs.sendAndWait(ctx, req, func(r *azdext.FrameworkServiceMessage) bool {
		return r.GetRequirementsResponse() != nil
	})
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

	resp, err := efs.sendAndWaitWithProgress(ctx, req, progress, func(r *azdext.FrameworkServiceMessage) bool {
		return r.GetRestoreResponse() != nil
	})
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

	resp, err := efs.sendAndWaitWithProgress(ctx, req, progress, func(r *azdext.FrameworkServiceMessage) bool {
		return r.GetBuildResponse() != nil
	})
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

	resp, err := efs.sendAndWaitWithProgress(ctx, req, progress, func(r *azdext.FrameworkServiceMessage) bool {
		return r.GetPackageResponse() != nil
	})
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

// Private methods for gRPC communication

// helper to send a request and wait for the matching response using async dispatcher
func (efs *ExternalFrameworkService) sendAndWait(
	ctx context.Context,
	req *azdext.FrameworkServiceMessage,
	match func(*azdext.FrameworkServiceMessage) bool,
) (*azdext.FrameworkServiceMessage, error) {
	// Create a response channel for this request
	respChan := make(chan *azdext.FrameworkServiceMessage, 1)
	efs.responseChans.Store(req.RequestId, respChan)
	defer efs.responseChans.Delete(req.RequestId)

	// Send the request
	if err := efs.stream.Send(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Wait for response via the async dispatcher
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-respChan:
		if !ok {
			return nil, fmt.Errorf("response channel closed")
		}

		if resp.Error != nil {
			return nil, fmt.Errorf("framework service error: %s", resp.Error.Message)
		}

		if !match(resp) {
			return nil, fmt.Errorf("received unexpected response type")
		}

		return resp, nil
	}
}

// helper to send a request, handle progress updates, and wait for the matching response
func (efs *ExternalFrameworkService) sendAndWaitWithProgress(
	ctx context.Context,
	req *azdext.FrameworkServiceMessage,
	progress *async.Progress[ServiceProgress],
	match func(*azdext.FrameworkServiceMessage) bool,
) (*azdext.FrameworkServiceMessage, error) {
	respChan := make(chan *azdext.FrameworkServiceMessage, 1)
	efs.responseChans.Store(req.RequestId, respChan)
	defer efs.responseChans.Delete(req.RequestId)

	if err := efs.stream.Send(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case resp := <-respChan:
			if resp == nil {
				return nil, fmt.Errorf("stream closed")
			}

			if resp.Error != nil {
				return nil, fmt.Errorf("framework service error: %s", resp.Error.Message)
			}

			if progressMsg := resp.GetProgressMessage(); progressMsg != nil {
				if progress != nil {
					progress.SetProgress(ServiceProgress{
						Message: progressMsg.Message,
					})
				}
				continue // Wait for the actual response
			}

			if !match(resp) {
				return nil, fmt.Errorf("received unexpected response type")
			}

			return resp, nil
		}
	}
}

// goroutine to receive and dispatch responses
func (efs *ExternalFrameworkService) startResponseDispatcher() {
	go func() {
		for {
			resp, err := efs.stream.Recv()
			if err != nil {
				// propagate error to all waiting calls
				efs.responseChans.Range(func(key, value any) bool {
					ch := value.(chan *azdext.FrameworkServiceMessage)
					close(ch)
					return true
				})
				return
			}
			if ch, ok := efs.responseChans.Load(resp.RequestId); ok {

				ch.(chan *azdext.FrameworkServiceMessage) <- resp
			} else {
				log.Printf("No response channel found for RequestId: %s", resp.RequestId)
			}
		}
	}()
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
