// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/google/uuid"
)

type ExternalServiceTarget struct {
	extension  *extensions.Extension
	targetName string
	targetKind ServiceTargetKind
	console    input.Console
	prompters  prompt.Prompter
	env        *environment.Environment

	stream        azdext.ServiceTargetService_StreamServer
	responseChans sync.Map
}

func envResolver(env *environment.Environment) mapper.Resolver {
	return func(key string) string {
		if env == nil {
			return ""
		}

		return env.Getenv(key)
	}
}

// Publish implements ServiceTarget.
func (est *ExternalServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	var protoServiceConfig *azdext.ServiceConfig
	err := mapper.WithResolver(envResolver(est.env)).Convert(serviceConfig, &protoServiceConfig)
	if err != nil {
		return nil, err
	}

	var protoServiceContext *azdext.ServiceContext
	if err := mapper.Convert(serviceContext, &protoServiceContext); err != nil {
		return nil, err
	}
	var protoTargetResource *azdext.TargetResource
	if err := mapper.Convert(targetResource, &protoTargetResource); err != nil {
		return nil, err
	}
	var protoPublishOptions *azdext.PublishOptions
	if err := mapper.Convert(publishOptions, &protoPublishOptions); err != nil {
		return nil, err
	}

	req := &azdext.ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ServiceTargetMessage_PublishRequest{
			PublishRequest: &azdext.ServiceTargetPublishRequest{
				ServiceConfig:  protoServiceConfig,
				ServiceContext: protoServiceContext,
				TargetResource: protoTargetResource,
				PublishOptions: protoPublishOptions,
			},
		},
	}

	resp, err := est.sendAndWaitWithProgress(ctx, req, progress, func(r *azdext.ServiceTargetMessage) bool {
		return r.GetPublishResponse() != nil
	})
	if err != nil {
		return nil, err
	}

	publishResp := resp.GetPublishResponse()
	if publishResp == nil || publishResp.Result == nil {
		return &ServicePublishResult{}, nil
	}

	var result *ServicePublishResult
	if err := mapper.Convert(publishResp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to convert publish result: %w", err)
	}

	return result, nil
}

// NewExternalServiceTarget creates a new external service target
func NewExternalServiceTarget(
	name string,
	kind ServiceTargetKind,
	extension *extensions.Extension,
	stream azdext.ServiceTargetService_StreamServer,
	console input.Console,
	prompters prompt.Prompter,
	env *environment.Environment,
) ServiceTarget {
	target := &ExternalServiceTarget{
		extension:  extension,
		targetName: name,
		targetKind: kind,
		console:    console,
		prompters:  prompters,
		env:        env,
		stream:     stream,
	}

	target.startResponseDispatcher()

	return target
}

// Initialize initializes the service target for the specified service configuration.
// This allows service targets to opt-in to service lifecycle events
func (est *ExternalServiceTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	if serviceConfig == nil {
		return errors.New("service configuration is required")
	}

	var protoServiceConfig *azdext.ServiceConfig
	err := mapper.WithResolver(envResolver(est.env)).Convert(serviceConfig, &protoServiceConfig)
	if err != nil {
		return err
	}

	req := &azdext.ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ServiceTargetMessage_InitializeRequest{
			InitializeRequest: &azdext.ServiceTargetInitializeRequest{
				ServiceConfig: protoServiceConfig,
			},
		},
	}

	_, err = est.sendAndWait(ctx, req, func(r *azdext.ServiceTargetMessage) bool {
		return r.GetInitializeResponse() != nil
	})
	return err
}

// RequiredExternalTools returns the tools needed to run the deploy operation for this target.
func (est *ExternalServiceTarget) RequiredExternalTools(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Package prepares artifacts for deployment
func (est *ExternalServiceTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	var protoServiceConfig *azdext.ServiceConfig
	err := mapper.WithResolver(envResolver(est.env)).Convert(serviceConfig, &protoServiceConfig)
	if err != nil {
		return nil, err
	}

	var protoServiceContext *azdext.ServiceContext
	if err := mapper.Convert(serviceContext, &protoServiceContext); err != nil {
		return nil, err
	}

	req := &azdext.ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ServiceTargetMessage_PackageRequest{
			PackageRequest: &azdext.ServiceTargetPackageRequest{
				ServiceConfig:  protoServiceConfig,
				ServiceContext: protoServiceContext,
			},
		},
	}

	resp, err := est.sendAndWaitWithProgress(ctx, req, progress, func(r *azdext.ServiceTargetMessage) bool {
		return r.GetPackageResponse() != nil
	})
	if err != nil {
		return nil, err
	}

	packageResp := resp.GetPackageResponse()
	if packageResp == nil || packageResp.Result == nil {
		return &ServicePackageResult{}, nil
	}

	// Convert proto result using mapper
	var convertedResult *ServicePackageResult
	if err := mapper.Convert(packageResp.Result, &convertedResult); err != nil {
		return nil, err
	}

	return convertedResult, nil
}

// Deploy deploys the given deployment artifact to the target resource
func (est *ExternalServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	// Convert project types to protobuf types
	var protoServiceConfig *azdext.ServiceConfig
	err := mapper.WithResolver(envResolver(est.env)).Convert(serviceConfig, &protoServiceConfig)
	if err != nil {
		return nil, err
	}

	var protoServiceContext *azdext.ServiceContext
	if err = mapper.Convert(serviceContext, &protoServiceContext); err != nil {
		return nil, err
	}
	var protoTargetResource *azdext.TargetResource
	if err = mapper.Convert(targetResource, &protoTargetResource); err != nil {
		return nil, err
	}

	// Create Deploy request message
	requestId := uuid.NewString()
	deployReq := &azdext.ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &azdext.ServiceTargetMessage_DeployRequest{
			DeployRequest: &azdext.ServiceTargetDeployRequest{
				ServiceConfig:  protoServiceConfig,
				ServiceContext: protoServiceContext,
				TargetResource: protoTargetResource,
			},
		},
	}

	// Send request and wait for response, handling progress messages
	resp, err := est.sendAndWaitWithProgress(ctx, deployReq, progress, func(r *azdext.ServiceTargetMessage) bool {
		return r.GetDeployResponse() != nil
	})

	if err != nil {
		return nil, err
	}

	deployResponse := resp.GetDeployResponse()
	if deployResponse == nil || deployResponse.Result == nil {
		return nil, errors.New("invalid deploy response: missing deploy result")
	}

	// Convert protobuf result back to project types using mapper
	var result *ServiceDeployResult
	if err := mapper.Convert(deployResponse.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to convert deploy result: %w", err)
	}

	return result, nil
}

// Endpoints gets the endpoints a service exposes.
func (est *ExternalServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	var protoServiceConfig *azdext.ServiceConfig
	err := mapper.WithResolver(envResolver(est.env)).Convert(serviceConfig, &protoServiceConfig)
	if err != nil {
		return nil, err
	}

	var protoTargetResource *azdext.TargetResource
	if err = mapper.Convert(targetResource, &protoTargetResource); err != nil {
		return nil, err
	}
	req := &azdext.ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ServiceTargetMessage_EndpointsRequest{
			EndpointsRequest: &azdext.ServiceTargetEndpointsRequest{
				ServiceConfig:  protoServiceConfig,
				TargetResource: protoTargetResource,
			},
		},
	}

	resp, err := est.sendAndWait(ctx, req, func(r *azdext.ServiceTargetMessage) bool {
		return r.GetEndpointsResponse() != nil
	})
	if err != nil {
		return nil, err
	}

	endpointsResp := resp.GetEndpointsResponse()
	if endpointsResp == nil {
		return []string{}, nil
	}

	return append([]string{}, endpointsResp.Endpoints...), nil

}

// ResolveTargetResource resolves the Azure target resource for the service configuration via the extension.
func (est *ExternalServiceTarget) ResolveTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *ServiceConfig,
	defaultResolver func() (*environment.TargetResource, error),
) (*environment.TargetResource, error) {
	var protoServiceConfig *azdext.ServiceConfig
	err := mapper.WithResolver(envResolver(est.env)).Convert(serviceConfig, &protoServiceConfig)
	if err != nil {
		return nil, err
	}

	// Compute the default target resource if a resolver is provided
	var protoDefaultTarget *azdext.TargetResource
	var defaultError string
	if defaultResolver != nil {
		defaultTarget, err := defaultResolver()
		if err != nil {
			// Capture error so extension can decide how to handle it
			defaultError = err.Error()
		} else if defaultTarget != nil {
			if err = mapper.Convert(defaultTarget, &protoDefaultTarget); err != nil {
				return nil, err
			}
		}
	}

	req := &azdext.ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ServiceTargetMessage_GetTargetResourceRequest{
			GetTargetResourceRequest: &azdext.GetTargetResourceRequest{
				SubscriptionId:        subscriptionId,
				ServiceConfig:         protoServiceConfig,
				DefaultTargetResource: protoDefaultTarget,
				DefaultError:          defaultError,
			},
		},
	}

	resp, err := est.sendAndWait(ctx, req, func(r *azdext.ServiceTargetMessage) bool {
		return r.GetGetTargetResourceResponse() != nil
	})
	if err != nil {
		return nil, err
	}

	result := resp.GetGetTargetResourceResponse()
	if result == nil || result.TargetResource == nil {
		return nil, errors.New("invalid get target resource response: missing target resource")
	}

	target := environment.NewTargetResource(
		result.TargetResource.SubscriptionId,
		result.TargetResource.ResourceGroupName,
		result.TargetResource.ResourceName,
		result.TargetResource.ResourceType,
	)
	target.SetMetadata(result.TargetResource.GetMetadata())

	return target, nil
}

// Private methods for gRPC communication

// helper to send a request and wait for the matching response
func (est *ExternalServiceTarget) sendAndWait(
	ctx context.Context,
	req *azdext.ServiceTargetMessage,
	match func(*azdext.ServiceTargetMessage) bool,
) (*azdext.ServiceTargetMessage, error) {
	ch := make(chan *azdext.ServiceTargetMessage, 1)
	est.responseChans.Store(req.RequestId, ch)
	defer est.responseChans.Delete(req.RequestId)

	if err := est.stream.Send(req); err != nil {
		return nil, err
	}

	for {
		select {
		case resp := <-ch:
			if match(resp) {
				if resp.Error != nil && resp.Error.Message != "" {
					return nil, errors.New(resp.Error.Message)
				}
				return resp, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// helper to send a request, handle progress updates, and wait for the matching response
func (est *ExternalServiceTarget) sendAndWaitWithProgress(
	ctx context.Context,
	req *azdext.ServiceTargetMessage,
	progress *async.Progress[ServiceProgress],
	match func(*azdext.ServiceTargetMessage) bool,
) (*azdext.ServiceTargetMessage, error) {
	// Use a larger buffer to handle multiple progress messages without blocking the dispatcher
	ch := make(chan *azdext.ServiceTargetMessage, 50)
	est.responseChans.Store(req.RequestId, ch)
	defer est.responseChans.Delete(req.RequestId)

	if err := est.stream.Send(req); err != nil {
		return nil, err
	}

	for {
		select {
		case resp := <-ch:
			// Check if this is a progress message
			if progressMsg := resp.GetProgressMessage(); progressMsg != nil && progressMsg.RequestId == req.RequestId {
				// Forward progress to core azd
				if progress != nil {
					progress.SetProgress(NewServiceProgress(progressMsg.Message))
				}
				// Continue waiting for more messages
				continue
			}

			// Check if this is the final response we're waiting for
			if match(resp) {
				if resp.Error != nil && resp.Error.Message != "" {
					return nil, errors.New(resp.Error.Message)
				}
				return resp, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// goroutine to receive and dispatch responses
func (est *ExternalServiceTarget) startResponseDispatcher() {
	go func() {
		for {
			resp, err := est.stream.Recv()
			if err != nil {
				// propagate error to all waiting calls
				est.responseChans.Range(func(key, value any) bool {
					ch := value.(chan *azdext.ServiceTargetMessage)
					close(ch)
					return true
				})
				return
			}
			if ch, ok := est.responseChans.Load(resp.RequestId); ok {
				ch.(chan *azdext.ServiceTargetMessage) <- resp
			}
		}
	}()
}
