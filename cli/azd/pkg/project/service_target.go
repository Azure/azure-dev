// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type ServiceTargetKind string

const (
	NonSpecifiedTarget       ServiceTargetKind = ""
	AppServiceTarget         ServiceTargetKind = "appservice"
	ContainerAppTarget       ServiceTargetKind = "containerapp"
	AzureFunctionTarget      ServiceTargetKind = "function"
	StaticWebAppTarget       ServiceTargetKind = "staticwebapp"
	SpringAppTarget          ServiceTargetKind = "springapp"
	AksTarget                ServiceTargetKind = "aks"
	DotNetContainerAppTarget ServiceTargetKind = "containerapp-dotnet"
	AiEndpointTarget         ServiceTargetKind = "ai.endpoint"
)

// DotNetContainerAppTarget is intentionally omitted because it is only used internally when
// containerizing .NET projects and is not a valid service host value in azure.yaml.
var builtInServiceTargetKinds = []ServiceTargetKind{
	AppServiceTarget,
	ContainerAppTarget,
	AzureFunctionTarget,
	StaticWebAppTarget,
	SpringAppTarget,
	AksTarget,
	AiEndpointTarget,
}

func builtInServiceTargetNames() []string {
	names := make([]string, 0, len(builtInServiceTargetKinds))
	for _, kind := range builtInServiceTargetKinds {
		names = append(names, string(kind))
	}

	return names
}

// BuiltInServiceTargetKinds returns the slice of built-in service target kinds
func BuiltInServiceTargetKinds() []ServiceTargetKind {
	return builtInServiceTargetKinds
}

// RequiresContainer returns true if the service target runs a container image.
func (stk ServiceTargetKind) RequiresContainer() bool {
	switch stk {
	case ContainerAppTarget,
		AksTarget:
		return true
	}

	return false
}

func parseServiceHost(kind ServiceTargetKind) (ServiceTargetKind, error) {
	// Allow any non-empty service target kind through.
	// Built-in kinds are handled by the hardcoded service target constructors in container.go
	// External kinds are handled by extensions that register via gRPC
	// If a kind is not supported, resolution will fail later with a clear error message
	if string(kind) != "" {
		return kind, nil
	}

	return ServiceTargetKind(""), fmt.Errorf("host cannot be empty")
}

type ServiceTarget interface {
	// Initializes the service target for the specified service configuration.
	// This allows service targets to opt-in to service lifecycle events
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error

	// RequiredExternalTools are the tools needed to run the deploy operation for this
	// target.
	RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool

	// Package prepares artifacts for deployment
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress *async.Progress[ServiceProgress],
	) (*ServicePackageResult, error)

	// Publish pushes the prepared artifacts without performing deployment.
	Publish(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		targetResource *environment.TargetResource,
		progress *async.Progress[ServiceProgress],
		publishOptions *PublishOptions,
	) (*ServicePublishResult, error)

	// Deploys the given deployment artifact to the target resource
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		targetResource *environment.TargetResource,
		progress *async.Progress[ServiceProgress],
	) (*ServiceDeployResult, error)

	// Endpoints gets the endpoints a service exposes.
	Endpoints(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		targetResource *environment.TargetResource,
	) ([]string, error)
}

func resourceTypeMismatchError(
	resourceName string,
	resourceType string,
	expectedResourceType azapi.AzureResourceType,
) error {
	return fmt.Errorf(
		"resource '%s' with type '%s' does not match expected resource type '%s'",
		resourceName,
		resourceType,
		string(expectedResourceType),
	)
}

// IgnoreFile returns the ignore file associated with the service target.
// Returns an empty string if no ignore file is used.
func (st ServiceTargetKind) IgnoreFile() string {
	switch st {
	case AppServiceTarget:
		return ".webappignore"
	case AzureFunctionTarget:
		return ".funcignore"
	default:
		return ""
	}
}

// SupportsDelayedProvisioning returns true if the service target kind
// supports delayed provisioning resources at deployment time, otherwise false.
//
// As an example, ContainerAppTarget is able to provision the container app as part of deployment,
// and thus returns true.
func (st ServiceTargetKind) SupportsDelayedProvisioning() bool {
	return st == AksTarget || st == ContainerAppTarget
}

func checkResourceType(resource *environment.TargetResource, expectedResourceType azapi.AzureResourceType) error {
	if !strings.EqualFold(resource.ResourceType(), string(expectedResourceType)) {
		return resourceTypeMismatchError(
			resource.ResourceName(),
			resource.ResourceType(),
			expectedResourceType,
		)
	}

	return nil
}
