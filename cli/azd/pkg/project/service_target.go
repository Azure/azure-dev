// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type ServiceTargetKind string

const (
	AppServiceTarget    ServiceTargetKind = "appservice"
	ContainerAppTarget  ServiceTargetKind = "containerapp"
	AzureFunctionTarget ServiceTargetKind = "function"
	StaticWebAppTarget  ServiceTargetKind = "staticwebapp"
	SpringAppTarget     ServiceTargetKind = "springapp"
	AksTarget           ServiceTargetKind = "aks"
)

func parseServiceHost(kind ServiceTargetKind) (ServiceTargetKind, error) {
	switch kind {
	case AppServiceTarget,
		ContainerAppTarget,
		AzureFunctionTarget,
		StaticWebAppTarget,
		SpringAppTarget,
		AksTarget:
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
		showProgress ShowProgress,
	) (ServicePackageResult, error)

	// Deploys the given deployment artifact to the target resource
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		servicePackage *ServicePackageResult,
		targetResource *environment.TargetResource,
		showProgress ShowProgress,
	) (ServiceDeployResult, error)

	// Endpoints gets the endpoints a service exposes.
	Endpoints(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		targetResource *environment.TargetResource,
	) ([]string, error)
}

func jsonStringOrUnmarshaled(value string) interface{} {
	var valueJson interface{}
	err := json.Unmarshal([]byte(value), &valueJson)
	if err != nil {
		return value
	}
	return valueJson
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
