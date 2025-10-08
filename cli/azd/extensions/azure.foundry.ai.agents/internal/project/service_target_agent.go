// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

// Reference implementation

// Ensure AgentServiceTargetProvider implements ServiceTargetProvider interface
var _ azdext.ServiceTargetProvider = &AgentServiceTargetProvider{}

// AgentServiceTargetProvider is a minimal implementation of ServiceTargetProvider for demonstration
type AgentServiceTargetProvider struct {
	azdClient           *azdext.AzdClient
	serviceConfig       *azdext.ServiceConfig
	agentDefinitionPath string
}

// NewAgentServiceTargetProvider creates a new AgentServiceTargetProvider instance
func NewAgentServiceTargetProvider(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &AgentServiceTargetProvider{
		azdClient: azdClient,
	}
}

// findYAMLFiles recursively searches for YAML/YML files in the given directory
func findYAMLFiles(rootPath string) ([]string, error) {
	var yamlFiles []string

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".yaml" || ext == ".yml" {
				yamlFiles = append(yamlFiles, path)
			}
		}
		return nil
	})

	return yamlFiles, err
}

// promptUserConfirmation asks the user to confirm if the found file is the agent definition
func promptUserConfirmation(filePath string) (bool, error) {
	fmt.Printf("Found agent definition file: %s\n", color.New(color.FgHiYellow).Sprint(filePath))
	fmt.Print("Is this the agent definition file you want to use? (y/N): ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes", nil
}

// promptUserSelection asks the user to select from multiple YAML files
func (p *AgentServiceTargetProvider) promptUserSelection(ctx context.Context, yamlFiles []string) (string, error) {
	fmt.Printf("Found multiple YAML/YML files:\n")
	for i, file := range yamlFiles {
		fmt.Printf("  %d. %s\n", i+1, color.New(color.FgHiYellow).Sprint(file))
	}

	fmt.Print("Please select the agent definition file (enter number): ")

	selectedFiles, err := p.azdClient.Prompt().Select("Select the agent definition file", yamlFiles, nil)
	if err != nil {
		return "", err
	}
	if len(selectedFiles.) == 0 {
		return "", fmt.Errorf("no file selected")
	}

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	response = strings.TrimSpace(response)
	selection, err := strconv.Atoi(response)
	if err != nil {
		return "", fmt.Errorf("invalid selection: %s", response)
	}

	if selection < 1 || selection > len(yamlFiles) {
		return "", fmt.Errorf("selection out of range: %d", selection)
	}

	return yamlFiles[selection-1], nil
}

// Initialize initializes the service target provider with service configuration
func (p *AgentServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	p.serviceConfig = serviceConfig

	proj, err := p.azdClient.Project().Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}
	servicePath := serviceConfig.RelativePath
	fullPath := filepath.Join(proj.Project.Path, servicePath)

	log.Println(proj.Project.Path, fullPath)

	// Check if user has specified agent definition path via environment variable
	if envPath := os.Getenv("FOUNDRY_AGENT_DEFINITION_PATH"); envPath != "" {
		// Verify the file exists and has correct extension
		if _, err := os.Stat(envPath); os.IsNotExist(err) {
			return fmt.Errorf("agent definition file specified in FOUNDRY_AGENT_DEFINITION_PATH does not exist: %s", envPath)
		}

		ext := strings.ToLower(filepath.Ext(envPath))
		if ext != ".yaml" && ext != ".yml" {
			return fmt.Errorf("agent definition file must be a YAML file (.yaml or .yml), got: %s", envPath)
		}

		p.agentDefinitionPath = envPath
		fmt.Printf("Using agent definition from environment variable: %s\n", color.New(color.FgHiGreen).Sprint(envPath))
		return nil
	}

	// Search for YAML/YML files in the service directory
	yamlFiles, err := findYAMLFiles(fullPath)
	if err != nil {
		return fmt.Errorf("failed to search for YAML files: %w", err)
	}

	switch len(yamlFiles) {
	case 0:
		return fmt.Errorf("no YAML/YML files found in %s. Please ensure an agent definition file exists or set FOUNDRY_AGENT_DEFINITION_PATH environment variable", fullPath)

	case 1:
		// Ask user to confirm if this is the agent definition file
		confirmed, err := promptUserConfirmation(yamlFiles[0])
		if err != nil {
			return fmt.Errorf("failed to get user confirmation: %w", err)
		}

		if !confirmed {
			return fmt.Errorf("user declined to use the found YAML file. Please set FOUNDRY_AGENT_DEFINITION_PATH environment variable to specify the correct agent definition file")
		}

		p.agentDefinitionPath = yamlFiles[0]
		fmt.Printf("Using agent definition: %s\n", color.New(color.FgHiGreen).Sprint(yamlFiles[0]))

	default:
		// Multiple files found, ask user to select
		selectedFile, err := promptUserSelection(yamlFiles)
		if err != nil {
			return fmt.Errorf("failed to get user selection: %w", err)
		}

		p.agentDefinitionPath = selectedFile
		fmt.Printf("Using selected agent definition: %s\n", color.New(color.FgHiGreen).Sprint(selectedFile))
	}

	return nil
} // Endpoints returns endpoints exposed by the agent service
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

	// E.g. Given `azd publish svc --to acr.io/my/img:tag12`, publishOptions.Image would be "acr.io/my/img:tag12"
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
	if packageResult.DockerPackageResult != nil {
		fmt.Printf("  Docker Package Result:\n")
		fmt.Printf("    Image Hash: %s\n", color.New(color.FgHiBlue).Sprint(packageResult.DockerPackageResult.ImageHash))
		fmt.Printf("    Source Image: %s\n", color.New(color.FgHiBlue).Sprint(packageResult.DockerPackageResult.SourceImage))
		fmt.Printf("    Target Image: %s\n", color.New(color.FgHiBlue).Sprint(packageResult.DockerPackageResult.TargetImage))
	}

	// Print publish result details
	fmt.Printf("\nPublish Details:\n")
	if publishResult.ContainerDetails != nil {
		fmt.Printf("  Remote Image: %s\n", color.New(color.FgHiBlue).Sprint(publishResult.ContainerDetails.RemoteImage))
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
