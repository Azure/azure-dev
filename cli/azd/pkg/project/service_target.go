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
	AppServiceTarget    ServiceTargetKind = "appservice"
	ContainerAppTarget  ServiceTargetKind = "containerapp"
	AzureFunctionTarget ServiceTargetKind = "function"
	StaticWebAppTarget  ServiceTargetKind = "staticwebapp"
	AksTarget           ServiceTargetKind = "aks"
)

type ServiceTarget interface {
	// RequiredExternalTools are the tools needed to run the deploy operation for this
	// target.
	RequiredExternalTools(ctx context.Context) []tools.ExternalTool
	// Package prepares artifacts for publishing
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		buildOutput *ServiceBuildResult,
	) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress]
	// Publish deploys the given deployment artifact to the target resource
	Publish(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		servicePackage *ServicePackageResult,
		targetResource *environment.TargetResource,
	) *async.TaskWithProgress[*ServicePublishResult, ServiceProgress]
	// Endpoints gets the endpoints a service exposes.
	Endpoints(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		targetResource *environment.TargetResource,
	) ([]string, error)
}

func NewServicePublishResult(
	relatedResourceId string,
	kind ServiceTargetKind,
	rawResult string,
	endpoints []string,
) *ServicePublishResult {
	returnValue := &ServicePublishResult{
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
	return st == ContainerAppTarget || st == AksTarget
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
