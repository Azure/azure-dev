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
	// Example: Call defaultResolver() if you want to use azd's default resource lookup
	// defaultTarget, err := defaultResolver()
	// if err != nil {
	//     return nil, err
	// }

	// For this demo, we completely override with custom logic
	targetResource := &azdext.TargetResource{
		SubscriptionId:    subscriptionId,
		ResourceGroupName: "rg-demo",
		ResourceName:      serviceConfig.Name + "-demo",
		ResourceType:      "Microsoft.Resources/resourceGroups",
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
	packageResponse, err := p.azdClient.Container().
		Package(ctx, &azdext.ContainerPackageRequest{
			ServiceName: serviceConfig.Name,
		})
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
			ServiceName: serviceConfig.Name,
			Package: &azdext.ServicePackageResult{
				Artifacts: []*azdext.Artifact{},
			},
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
	progress("Deploying service")
	time.Sleep(1000 * time.Millisecond)

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

	// Return deployment result with artifacts
	deployResult := &azdext.ServiceDeployResult{
		Artifacts: []*azdext.Artifact{
			{
				Kind:         "deployment",
				Location:     resourceId,
				LocationKind: "remote",
				Metadata: map[string]string{
					"kind":      "demo",
					"endpoints": fmt.Sprintf("%v", endpoints),
					"message":   "Service deployed successfully",
				},
			},
		},
	}

	return deployResult, nil
}
