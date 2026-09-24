// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
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
	lazyEnv    *lazy.Lazy[*environment.Environment]

	broker *grpcbroker.MessageBroker[azdext.ServiceTargetMessage]
}

type TargetResourceResolver interface {
	ResolveTargetResource(
		ctx context.Context,
		subscriptionId string,
		serviceConfig *ServiceConfig,
		defaultResolver func() (*environment.TargetResource, error),
	) (*environment.TargetResource, error)
}

// NewExternalServiceTarget creates a new external service target
func NewExternalServiceTarget(
	name string,
	kind ServiceTargetKind,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[azdext.ServiceTargetMessage],
	console input.Console,
	prompters prompt.Prompter,
	lazyEnv *lazy.Lazy[*environment.Environment],
) ServiceTarget {
	target := &ExternalServiceTarget{
		extension:  extension,
		targetName: name,
		targetKind: kind,
		console:    console,
		prompters:  prompters,
		lazyEnv:    lazyEnv,
		broker:     broker,
	}

	return target
}

// toProtoServiceConfig converts a ServiceConfig to its proto representation, expanding
// expandable values against the environment for the current session.
func (est *ExternalServiceTarget) toProtoServiceConfig(serviceConfig *ServiceConfig) (*azdext.ServiceConfig, error) {
	return serviceConfigToProto(est.lazyEnv, serviceConfig)
}

func (est *ExternalServiceTarget) wrapInvocationError(err error, operation string) error {
	if err == nil || est.extension == nil {
		return err
	}

	return extensions.WrapInvocationError(
		err,
		est.extension.Id,
		est.extension.Version,
		"service_target."+operation,
	)
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
	protoServiceConfig, err := est.toProtoServiceConfig(serviceConfig)
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

	resp, err := est.broker.SendAndWaitWithProgress(ctx, req, createProgressFunc(progress))
	if err != nil {
		return nil, est.wrapInvocationError(err, "publish")
	}

	publishResp := resp.GetPublishResponse()
	if publishResp == nil || publishResp.Result == nil {
		return &ServicePublishResult{}, nil
	}

	var result *ServicePublishResult
	if err := mapper.Convert(publishResp.Result, &result); err != nil {
		return nil, est.wrapInvocationError(
			fmt.Errorf("failed to convert publish result: %w", err),
			"publish",
		)
	}

	return result, nil
}

// Initialize initializes the service target for the specified service configuration.
// This allows service targets to opt-in to service lifecycle events
func (est *ExternalServiceTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
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

	_, err = est.broker.SendAndWait(ctx, req)
	return est.wrapInvocationError(err, "initialize")
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
	protoServiceConfig, err := est.toProtoServiceConfig(serviceConfig)
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

	resp, err := est.broker.SendAndWaitWithProgress(ctx, req, createProgressFunc(progress))
	if err != nil {
		return nil, est.wrapInvocationError(err, "package")
	}

	packageResp := resp.GetPackageResponse()
	if packageResp == nil || packageResp.Result == nil {
		return &ServicePackageResult{}, nil
	}

	// Convert proto result using mapper
	var convertedResult *ServicePackageResult
	if err := mapper.Convert(packageResp.Result, &convertedResult); err != nil {
		return nil, est.wrapInvocationError(
			fmt.Errorf("failed to convert package result: %w", err),
			"package",
		)
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
	protoServiceConfig, err := est.toProtoServiceConfig(serviceConfig)
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
	req := &azdext.ServiceTargetMessage{
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
	resp, err := est.broker.SendAndWaitWithProgress(ctx, req, createProgressFunc(progress))
	if err != nil {
		return nil, est.wrapInvocationError(err, "deploy")
	}

	deployResponse := resp.GetDeployResponse()
	if deployResponse == nil || deployResponse.Result == nil {
		return nil, est.wrapInvocationError(
			&ExternalServiceTargetResponseError{
				Operation: "deploy",
				Detail:    "missing deploy result",
			},
			"deploy",
		)
	}

	// Convert protobuf result back to project types using mapper
	var result *ServiceDeployResult
	if err := mapper.Convert(deployResponse.Result, &result); err != nil {
		return nil, est.wrapInvocationError(
			fmt.Errorf("failed to convert deploy result: %w", err),
			"deploy",
		)
	}

	return result, nil
}

// Endpoints gets the endpoints a service exposes.
func (est *ExternalServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	protoServiceConfig, err := est.toProtoServiceConfig(serviceConfig)
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

	resp, err := est.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil, est.wrapInvocationError(err, "endpoints")
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

	resp, err := est.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil, est.wrapInvocationError(err, "get_target_resource")
	}

	result := resp.GetGetTargetResourceResponse()
	if result == nil || result.TargetResource == nil {
		return nil, est.wrapInvocationError(
			&ExternalServiceTargetResponseError{
				Operation: "get target resource",
				Detail:    "missing target resource",
			},
			"get_target_resource",
		)
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

// ExternalServiceTargetResponseError reports a malformed response from an extension service-target provider.
type ExternalServiceTargetResponseError struct {
	Operation string
	Detail    string
}

func (e *ExternalServiceTargetResponseError) Error() string {
	return fmt.Sprintf("invalid %s response: %s", e.Operation, e.Detail)
}

func envResolver(env *environment.Environment) mapper.Resolver {
	return func(key string) string {
		if env == nil {
			return ""
		}

		return env.Getenv(key)
	}
}

// serviceConfigToProto converts a ServiceConfig to its proto representation for an external
// provider, resolving the environment from lazyEnv at call time so values reflect the current
// session environment. When the environment cannot be loaded, the failure is logged and
// expandable values are expanded with empty strings.
func serviceConfigToProto(
	lazyEnv *lazy.Lazy[*environment.Environment],
	serviceConfig *ServiceConfig,
) (*azdext.ServiceConfig, error) {
	if serviceConfig == nil {
		return nil, nil
	}

	var env *environment.Environment
	if lazyEnv != nil {
		var err error
		env, err = lazyEnv.GetValue()
		if err != nil {
			log.Printf(
				"converting service config %q: environment unavailable, expanding with empty values: %v",
				serviceConfig.Name,
				err,
			)
		}
	}

	var protoConfig *azdext.ServiceConfig
	if err := mapper.WithResolver(envResolver(env)).Convert(serviceConfig, &protoConfig); err != nil {
		return nil, fmt.Errorf("converting service config: %w", err)
	}

	return protoConfig, nil
}

func createProgressFunc(progress *async.Progress[ServiceProgress]) func(string) {
	return func(message string) {
		if progress != nil {
			progress.SetProgress(NewServiceProgress(message))
		}
	}
}
