// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

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

// Publish implements ServiceTarget.
func (est *ExternalServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	frameworkPackageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	cleanup := est.wireConsole()
	defer cleanup()

	protoServiceConfig, err := est.toProtoServiceConfig(serviceConfig)
	if err != nil {
		return nil, err
	}

	protoServicePackage := toProtoServicePackageResult(frameworkPackageOutput)
	protoTargetResource := toProtoTargetResource(targetResource)

	req := &azdext.ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ServiceTargetMessage_PublishRequest{
			PublishRequest: &azdext.ServiceTargetPublishRequest{
				ServiceConfig:  protoServiceConfig,
				ServicePackage: protoServicePackage,
				TargetResource: protoTargetResource,
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
	result := &ServicePublishResult{
		Package: frameworkPackageOutput,
	}

	if publishResp != nil && publishResp.PublishResult != nil {
		result.Details = stringMapToDetailsInterface(publishResp.PublishResult.Details)
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
	cleanup := est.wireConsole()
	defer cleanup()

	if serviceConfig == nil {
		return errors.New("service configuration is required")
	}

	protoServiceConfig, err := est.toProtoServiceConfig(serviceConfig)
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
	frameworkPackageOutput *ServicePackageResult,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	cleanup := est.wireConsole()
	defer cleanup()

	protoServiceConfig, err := est.toProtoServiceConfig(serviceConfig)
	if err != nil {
		return nil, err
	}

	protoFrameworkPackage := toProtoServicePackageResult(frameworkPackageOutput)

	req := &azdext.ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ServiceTargetMessage_PackageRequest{
			PackageRequest: &azdext.ServiceTargetPackageRequest{
				ServiceConfig:    protoServiceConfig,
				FrameworkPackage: protoFrameworkPackage,
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
	if packageResp == nil || packageResp.PackageResult == nil {
		return frameworkPackageOutput, nil
	}

	result := fromProtoServicePackageResult(packageResp.PackageResult, frameworkPackageOutput)
	return result, nil
}

// Deploy deploys the given deployment artifact to the target resource
func (est *ExternalServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	servicePackage *ServicePackageResult,
	publishResult *ServicePublishResult,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	cleanup := est.wireConsole()
	defer cleanup()

	// Convert project types to protobuf types
	protoServiceConfig, err := est.toProtoServiceConfig(serviceConfig)
	if err != nil {
		return nil, err
	}

	protoServicePackage := toProtoServicePackageResult(servicePackage)
	protoServicePublish := toProtoServicePublishResult(publishResult)
	protoTargetResource := toProtoTargetResource(targetResource)

	// Create Deploy request message
	requestId := uuid.NewString()
	deployReq := &azdext.ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &azdext.ServiceTargetMessage_DeployRequest{
			DeployRequest: &azdext.ServiceTargetDeployRequest{
				ServiceConfig:  protoServiceConfig,
				ServicePackage: protoServicePackage,
				TargetResource: protoTargetResource,
				ServicePublish: protoServicePublish,
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
	if deployResponse == nil || deployResponse.DeployResult == nil {
		return nil, errors.New("invalid deploy response: missing deploy result")
	}

	// Convert protobuf result back to project types
	result := deployResponse.DeployResult

	return &ServiceDeployResult{
		Package:          servicePackage,
		Publish:          publishResult,
		TargetResourceId: result.TargetResourceId,
		Kind:             ServiceTargetKind(result.Kind),
		Endpoints:        result.Endpoints,
		Details:          stringMapToDetailsInterface(result.Details),
	}, nil
}

// Endpoints gets the endpoints a service exposes.
func (est *ExternalServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	cleanup := est.wireConsole()
	defer cleanup()

	protoServiceConfig, err := est.toProtoServiceConfig(serviceConfig)
	if err != nil {
		return nil, err
	}

	protoTargetResource := toProtoTargetResource(targetResource)
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
	cleanup := est.wireConsole()
	defer cleanup()

	protoServiceConfig, err := est.toProtoServiceConfig(serviceConfig)
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
			protoDefaultTarget = toProtoTargetResource(defaultTarget)
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

func (est *ExternalServiceTarget) wireConsole() func() {
	stdOut := est.extension.StdOut()
	stdErr := est.extension.StdErr()
	stdOut.AddWriter(est.console.Handles().Stdout)
	stdErr.AddWriter(est.console.Handles().Stderr)

	return func() {
		stdOut.RemoveWriter(est.console.Handles().Stdout)
		stdErr.RemoveWriter(est.console.Handles().Stderr)
	}
}

func (est *ExternalServiceTarget) toProtoServiceConfig(serviceConfig *ServiceConfig) (*azdext.ServiceConfig, error) {
	resolver := func(env string) string {
		return ""
	}

	if est.env != nil {
		resolver = est.env.Getenv
	}

	resourceGroupName, err := serviceConfig.ResourceGroupName.Envsubst(resolver)
	if err != nil {
		return nil, fmt.Errorf("envsubst service resource group name: %w", err)
	}

	resourceName, err := serviceConfig.ResourceName.Envsubst(resolver)
	if err != nil {
		return nil, fmt.Errorf("envsubst service resource name: %w", err)
	}

	image, err := serviceConfig.Image.Envsubst(resolver)
	if err != nil {
		return nil, fmt.Errorf("envsubst image: %w", err)
	}

	return &azdext.ServiceConfig{
		Name:              serviceConfig.Name,
		ResourceGroupName: resourceGroupName,
		ResourceName:      resourceName,
		ApiVersion:        serviceConfig.ApiVersion,
		RelativePath:      serviceConfig.RelativePath,
		Host:              string(serviceConfig.Host),
		Language:          string(serviceConfig.Language),
		OutputPath:        serviceConfig.OutputPath,
		Image:             image,
	}, nil
}

func toProtoServicePackageResult(result *ServicePackageResult) *azdext.ServicePackageResult {
	if result == nil {
		return nil
	}

	details := detailsInterfaceToStringMap(result.Details)
	protoResult := &azdext.ServicePackageResult{PackagePath: result.PackagePath}
	if len(details) > 0 {
		protoResult.Details = details
	}

	return protoResult
}

func fromProtoServicePackageResult(
	protoResult *azdext.ServicePackageResult,
	fallback *ServicePackageResult,
) *ServicePackageResult {
	if protoResult == nil {
		return fallback
	}

	result := &ServicePackageResult{}
	if fallback != nil {
		result.PackagePath = fallback.PackagePath
		result.Details = fallback.Details
	}

	if protoResult.PackagePath != "" {
		result.PackagePath = protoResult.PackagePath
	}

	if len(protoResult.Details) > 0 {
		result.Details = stringMapToDetailsInterface(protoResult.Details)
	}

	return result
}

func toProtoServicePublishResult(result *ServicePublishResult) *azdext.ServicePublishResult {
	if result == nil {
		return nil
	}

	details := detailsInterfaceToStringMap(result.Details)
	if len(details) == 0 {
		return nil
	}

	return &azdext.ServicePublishResult{
		Details: details,
	}
}

func toProtoTargetResource(target *environment.TargetResource) *azdext.TargetResource {
	if target == nil {
		return nil
	}

	protoTarget := &azdext.TargetResource{
		SubscriptionId:    target.SubscriptionId(),
		ResourceGroupName: target.ResourceGroupName(),
		ResourceName:      target.ResourceName(),
		ResourceType:      target.ResourceType(),
	}

	if metadata := target.Metadata(); metadata != nil {
		protoTarget.Metadata = metadata
	}

	return protoTarget
}

func detailsInterfaceToStringMap(details interface{}) map[string]string {
	if details == nil {
		return nil
	}

	// Fast path for already-converted maps
	if m, ok := details.(map[string]string); ok {
		return m
	}

	// Use JSON as the serialization format for all types
	data, err := json.Marshal(details)
	if err != nil {
		// Fallback: convert to string representation
		value := fmt.Sprint(details)
		if value == "" || value == "<nil>" {
			return nil
		}
		return map[string]string{"value": value}
	}

	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		// Fallback
		return map[string]string{"json": string(data)}
	}

	if len(result) == 0 {
		return nil
	}

	return result
}

func stringMapToDetailsInterface(details map[string]string) interface{} {
	if len(details) == 0 {
		return nil
	}

	return details
}
