// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
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
	switch kind {

	// NOTE: We do not support DotNetContainerAppTarget as a listed service host type in azure.yaml, hence
	// it not include in this switch statement. We should think about if we should support this in azure.yaml because
	// presently it's the only service target that is tied to a language.
	case AppServiceTarget,
		ContainerAppTarget,
		AzureFunctionTarget,
		StaticWebAppTarget,
		SpringAppTarget,
		AksTarget,
		AiEndpointTarget:

		return kind, nil
	}

	return ServiceTargetKind(""), fmt.Errorf("unsupported host '%s'", kind)
}

type ServiceTarget interface {
	// Initializes the service target for the specified service configuration.
	// This allows service targets to opt-in to service lifecycle events
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error

	// RequiredExternalTools are the tools needed to run the deploy operation for this
	// target.
	RequiredExternalTools(ctx context.Context) []tools.ExternalTool

	// Package prepares artifacts for deployment
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		frameworkPackageOutput *ServicePackageResult,
	) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress]

	// Deploys the given deployment artifact to the target resource
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		servicePackage *ServicePackageResult,
		targetResource *environment.TargetResource,
	) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress]

	// Endpoints gets the endpoints a service exposes.
	Endpoints(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		targetResource *environment.TargetResource,
	) ([]string, error)
}

// NewServiceDeployResult is a helper function to create a new ServiceDeployResult
func NewServiceDeployResult(
	relatedResourceId string,
	kind ServiceTargetKind,
	rawResult string,
	endpoints []string,
) *ServiceDeployResult {
	returnValue := &ServiceDeployResult{
		TargetResourceId: relatedResourceId,
		Kind:             kind,
		Endpoints:        endpoints,
	}

	// If the result can be parsed as JSON, store it as such.
	// Otherwise, just preserve in raw (string) format.
	var detailsObj interface{}
	err := json.Unmarshal([]byte(rawResult), &detailsObj)
	if err != nil {
		returnValue.Details = rawResult
	} else {
		returnValue.Details = detailsObj
	}

	return returnValue
}

func resourceTypeMismatchError(
	resourceName string,
	resourceType string,
	expectedResourceType infra.AzureResourceType,
) error {
	return fmt.Errorf(
		"resource '%s' with type '%s' does not match expected resource type '%s'",
		resourceName,
		resourceType,
		string(expectedResourceType),
	)
}

// SupportsDelayedProvisioning returns true if the service target kind
// supports delayed provisioning resources at deployment time, otherwise false.
//
// As an example, ContainerAppTarget is able to provision the container app as part of deployment,
// and thus returns true.
func (st ServiceTargetKind) SupportsDelayedProvisioning() bool {
	return st == AksTarget
}

func checkResourceType(resource *environment.TargetResource, expectedResourceType infra.AzureResourceType) error {
	if !strings.EqualFold(resource.ResourceType(), string(expectedResourceType)) {
		return resourceTypeMismatchError(
			resource.ResourceName(),
			resource.ResourceType(),
			expectedResourceType,
		)
	}

	return nil
}
