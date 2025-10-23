// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Ensure DemoServiceTargetProvider implements ServiceTargetProvider interface
var _ azdext.ServiceTargetProvider = &DemoServiceTargetProvider{}

// DemoServiceTargetProvider is a minimal implementation of ServiceTargetProvider for demonstration
type DemoServiceTargetProvider struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
}

// NewDemoServiceTargetProvider creates a new DemoServiceTargetProvider instance
func NewDemoServiceTargetProvider(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &DemoServiceTargetProvider{
		azdClient: azdClient,
	}
}

// Initialize initializes the service target provider with service configuration
func (p *DemoServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints returns endpoints exposed by the service
func (p *DemoServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return []string{
		fmt.Sprintf("https://%s.example.com", targetResource.ResourceName),
	}, nil
}

// GetTargetResource returns a target resource for the service
func (p *DemoServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	targetResource, err := defaultResolver()
	if err != nil {
		// For this demo, we completely override with custom logic
		targetResource = &azdext.TargetResource{
			SubscriptionId:    subscriptionId,
			ResourceGroupName: serviceConfig.ResourceGroupName,
			ResourceName:      serviceConfig.ResourceName,
		}
	}

	return targetResource, nil
}

// Package performs packaging for the service
func (p *DemoServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	var containerArtifact *azdext.Artifact

	for _, artifact := range serviceContext.Package {
		if artifact.Kind == azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER {
			containerArtifact = artifact
			break
		}
	}

	if containerArtifact == nil {
		buildResponse, err := p.azdClient.Container().
			Build(ctx, &azdext.ContainerBuildRequest{
				ServiceName:    serviceConfig.Name,
				ServiceContext: serviceContext,
			},
			)
		if err != nil {
			return nil, err
		}

		serviceContext.Build = append(serviceContext.Build, buildResponse.Result.Artifacts...)
	}

	packageResponse, err := p.azdClient.Container().
		Package(ctx, &azdext.ContainerPackageRequest{
			ServiceName:    serviceConfig.Name,
			ServiceContext: serviceContext,
		},
		)
	if err != nil {
		return nil, err
	}

	return &azdext.ServicePackageResult{
		Artifacts: packageResponse.Result.Artifacts,
	}, nil
}

// Publish performs the publish operation for the service
func (p *DemoServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	publishResponse, err := p.azdClient.Container().
		Publish(ctx, &azdext.ContainerPublishRequest{
			ServiceName:    serviceConfig.Name,
			ServiceContext: serviceContext,
		})
	if err != nil {
		return nil, err
	}

	return &azdext.ServicePublishResult{
		Artifacts: publishResponse.Result.Artifacts,
	}, nil
}

// Deploy performs the deployment operation for the service
func (p *DemoServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	progress("Deploying demo service")
	time.Sleep(5 * time.Second)

	// Construct resource ID
	resourceId := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s",
		targetResource.SubscriptionId,
		targetResource.ResourceGroupName,
		targetResource.ResourceType,
		targetResource.ResourceName)

	// Resolve endpoints
	endpoints, err := p.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	artifacts := []*azdext.Artifact{
		{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_RESOURCE,
			Location:     resourceId,
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			Metadata: map[string]string{
				"subscription":  targetResource.SubscriptionId,
				"resourceGroup": targetResource.ResourceGroupName,
				"type":          targetResource.ResourceType,
				"name":          targetResource.ResourceName,
			},
		},
	}

	for _, endpoint := range endpoints {
		artifacts = append(artifacts, &azdext.Artifact{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
			Location:     endpoint,
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
		})
	}

	// Return deployment result with artifacts
	deployResult := &azdext.ServiceDeployResult{
		Artifacts: artifacts,
	}

	return deployResult, nil
}
