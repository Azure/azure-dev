// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

// Reference implementation

// Ensure AgentServiceTargetProvider implements ServiceTargetProvider interface
var _ azdext.ServiceTargetProvider = &AgentServiceTargetProvider{}

// AgentServiceTargetProvider is a minimal implementation of ServiceTargetProvider for demonstration
type AgentServiceTargetProvider struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
}

// NewAgentServiceTargetProvider creates a new AgentServiceTargetProvider instance
func NewAgentServiceTargetProvider(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &AgentServiceTargetProvider{
		azdClient: azdClient,
	}
}

// Initialize initializes the service target provider with service configuration
func (p *AgentServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	if serviceConfig != nil {
		serviceName := color.New(color.FgHiBlue).Sprint(serviceConfig.GetName())
		fmt.Printf("Agent extension initializing for service: %s\n", serviceName)
	}
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints returns endpoints exposed by the agent service
func (p *AgentServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return []string{
		fmt.Sprintf("https://%s.%s.azurecontainerapps.io/api", targetResource.ResourceName, "region"),
	}, nil
}

// GetTargetResource returns a custom target resource for the agent service
func (p *AgentServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	serviceName := ""
	if serviceConfig != nil {
		serviceName = serviceConfig.Name
	}
	targetResource := &azdext.TargetResource{
		SubscriptionId:    subscriptionId,
		ResourceGroupName: "rg-agent-demo",
		ResourceName:      "ca-" + serviceName + "-agent",
		ResourceType:      "Microsoft.App/containerApps",
		Metadata: map[string]string{
			"agentId":   "asst_xYZ",
			"agentName": "Agent 007",
		},
	}

	fmt.Printf("Agent target resource: %s\n", color.New(color.FgHiBlue).Sprint(targetResource.ResourceName))

	// Log all fields of ServiceConfig
	if serviceConfig != nil {
		fmt.Printf("Service Config Details:\n")
		fmt.Printf("  Name: %s\n", color.New(color.FgHiBlue).Sprint(serviceConfig.Name))
		fmt.Printf("  ResourceGroupName: %s\n", color.New(color.FgHiBlue).Sprint(serviceConfig.ResourceGroupName))
		fmt.Printf("  ResourceName: %s\n", color.New(color.FgHiBlue).Sprint(serviceConfig.ResourceName))
		fmt.Printf("  ApiVersion: %s\n", color.New(color.FgHiBlue).Sprint(serviceConfig.ApiVersion))
		fmt.Printf("  RelativePath: %s\n", color.New(color.FgHiBlue).Sprint(serviceConfig.RelativePath))
		fmt.Printf("  Host: %s\n", color.New(color.FgHiBlue).Sprint(serviceConfig.Host))
		fmt.Printf("  Language: %s\n", color.New(color.FgHiBlue).Sprint(serviceConfig.Language))
		fmt.Printf("  OutputPath: %s\n", color.New(color.FgHiBlue).Sprint(serviceConfig.OutputPath))
		fmt.Printf("  Image: %s\n", color.New(color.FgHiBlue).Sprint(serviceConfig.Image))

		fmt.Println()
	}
	return targetResource, nil
}

// Package performs packaging for the agent service
func (p *AgentServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	frameworkPackageOutput *azdext.ServicePackageResult,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	var targetImage string

	// Check for structured docker package result first
	if frameworkPackageOutput.DockerPackageResult != nil {
		targetImage = frameworkPackageOutput.DockerPackageResult.TargetImage
	}

	fmt.Printf("\nPackage path: %s\n", color.New(color.FgHiBlue).Sprint(frameworkPackageOutput.PackagePath))
	fmt.Printf("\nDockerPackageResult: %s\n", color.New(color.FgHiBlue).Sprint(targetImage))

	return &azdext.ServicePackageResult{
		PackagePath:         frameworkPackageOutput.PackagePath,
		DockerPackageResult: frameworkPackageOutput.DockerPackageResult,
		Details: map[string]string{
			"timestamp": time.Now().Format(time.RFC3339),
		},
	}, nil
}

// Publish performs the publish operation for the agent service
func (p *AgentServiceTargetProvider) Publish(
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

	if packageResult.DockerPackageResult == nil {
		return nil, fmt.Errorf("docker package result is nil")
	}

	localImageTag := packageResult.DockerPackageResult.TargetImage

	// E.g. Given `azd publish svc --to myreg.io/my/img:tag12`, publishOptions.Image would be "myreg.io/my/img:tag12"
	if publishOptions != nil && publishOptions.Image != "" {
		// To actually use this, you may need to parse out the registry, image name, and tag components
		// See parseImageOverride in container_helper.go
		fmt.Printf("Using publish options with image: %s\n", publishOptions.Image)
	}

	remoteImage := fmt.Sprintf("contoso.azurecr.io/%s", localImageTag)
	fmt.Printf("\nAgent image published: %s\n", color.New(color.FgHiBlue).Sprint(remoteImage))

	return &azdext.ServicePublishResult{
		ContainerDetails: &azdext.ContainerPublishDetails{
			RemoteImage: remoteImage,
		},
	}, nil
}

// Deploy performs the deployment operation for the agent service
func (p *AgentServiceTargetProvider) Deploy(
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

	progress("Parsing details")
	time.Sleep(1000 * time.Millisecond) // Simulate work

	// Print package result details
	fmt.Printf("\nPackage Details:\n")
	fmt.Printf("  Package Path: %s\n", color.New(color.FgHiBlue).Sprint(packageResult.GetPackagePath()))
	if packageResult.Details != nil {
		for key, value := range packageResult.Details {
			fmt.Printf("  %s: %s\n", key, color.New(color.FgHiBlue).Sprint(value))
		}
	}

	// Print publish result details
	fmt.Printf("\nPublish Details:\n")
	if publishResult.Details != nil {
		for key, value := range publishResult.Details {
			fmt.Printf("  %s: %s\n", key, color.New(color.FgHiBlue).Sprint(value))
		}
	}
	fmt.Println()

	progress("Deploying service to target resource")
	time.Sleep(2000 * time.Millisecond) // Simulate work

	progress("Verifying deployment health")
	time.Sleep(1000 * time.Millisecond) // Simulate work

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
		Kind:             "agent",
		Endpoints:        endpoints,
		Details: map[string]string{
			"message": "Agent service deployed successfully using custom extension logic",
		},
	}

	return deployResult, nil
}
