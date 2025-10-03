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
	if serviceConfig != nil {
		fmt.Printf("Demo extension initializing for service: %s\n", serviceConfig.GetName())
	}
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

	fmt.Printf("Target resource: %s\n", targetResource.ResourceName)
	return targetResource, nil
}

// Package performs packaging for the service
func (p *DemoServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	packageResult *azdext.ServicePackageResult,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	progress("Preparing package")
	time.Sleep(500 * time.Millisecond)

	packagePath := "demo/package:latest"
	fmt.Printf("\nPackage created: %s\n", packagePath)

	return &azdext.ServicePackageResult{
		PackagePath: packagePath,
		Details: map[string]string{
			"timestamp": time.Now().Format(time.RFC3339),
		},
	}, nil
}

// Publish performs the publish operation for the service
func (p *DemoServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	packageResult *azdext.ServicePackageResult,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	if packageResult == nil {
		return nil, fmt.Errorf("packageResult is nil")
	}

	packagePath := packageResult.GetPackagePath()
	if packagePath == "" {
		return nil, fmt.Errorf("package path is empty")
	}

	progress(fmt.Sprintf("Publishing %s...", packagePath))
	time.Sleep(500 * time.Millisecond)

	remotePackage := fmt.Sprintf("registry.example.com/%s", packagePath)
	fmt.Printf("\nPackage published: %s\n", remotePackage)

	return &azdext.ServicePublishResult{
		Details: map[string]string{
			"remotePackage": remotePackage,
		},
	}, nil
}

// Deploy performs the deployment operation for the service
func (p *DemoServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	packageResult *azdext.ServicePackageResult,
	publishResult *azdext.ServicePublishResult,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	if packageResult == nil {
		return nil, fmt.Errorf("packageResult is nil")
	}

	if publishResult == nil {
		return nil, fmt.Errorf("publishResult is nil")
	}

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

	// Return deployment result
	deployResult := &azdext.ServiceDeployResult{
		TargetResourceId: resourceId,
		Kind:             "demo",
		Endpoints:        endpoints,
		Details: map[string]string{
			"message": "Service deployed successfully",
		},
	}

	return deployResult, nil
}
