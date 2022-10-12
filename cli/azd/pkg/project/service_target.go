// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type ServiceTargetKind string

const (
	AppServiceTarget    ServiceTargetKind = "appservice"
	ContainerAppTarget  ServiceTargetKind = "containerapp"
	AzureFunctionTarget ServiceTargetKind = "function"
	StaticWebAppTarget  ServiceTargetKind = "staticwebapp"
)

type ServiceDeploymentResult struct {
	// Related Azure resource ID
	TargetResourceId string            `json:"targetResourceId"`
	Kind             ServiceTargetKind `json:"kind"`
	Details          interface{}       `json:"details"`
	Endpoints        []string          `json:"endpoints"`
}

type ServiceTarget interface {
	// RequiredExternalTools are the tools needed to run the deploy operation for this
	// target.
	RequiredExternalTools() []tools.ExternalTool
	// Deploy deploys the given deployment artifact to the target resource
	Deploy(
		ctx context.Context,
		azdCtx *azdcontext.AzdContext,
		path string,
		progress chan<- string,
	) (ServiceDeploymentResult, error)
	// Endpoints gets the endpoints a service exposes.
	Endpoints(ctx context.Context) ([]string, error)
}

func NewServiceDeploymentResult(
	relatedResourceId string,
	kind ServiceTargetKind,
	rawResult string,
	endpoints []string,
) ServiceDeploymentResult {
	returnValue := ServiceDeploymentResult{
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

var _ ServiceTarget = &appServiceTarget{}
var _ ServiceTarget = &containerAppTarget{}
var _ ServiceTarget = &functionAppTarget{}
var _ ServiceTarget = &staticWebAppTarget{}
