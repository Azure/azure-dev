// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
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

// Initialize initializes the service target by looking for the agent definition file
func (p *AgentServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	if p.agentDefinitionPath != "" {
		// Already initialized
		return nil
	}

	p.serviceConfig = serviceConfig

	proj, err := p.azdClient.Project().Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}
	servicePath := serviceConfig.RelativePath
	fullPath := filepath.Join(proj.Project.Path, servicePath)

	fmt.Fprintf(os.Stderr, "Project path: %s, Service path: %s\n", proj.Project.Path, fullPath)

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

	// Look for agent.yaml or agent.yml in the service directory root
	agentYamlPath := filepath.Join(fullPath, "agent.yaml")
	agentYmlPath := filepath.Join(fullPath, "agent.yml")

	if _, err := os.Stat(agentYamlPath); err == nil {
		p.agentDefinitionPath = agentYamlPath
		fmt.Printf("Using agent definition: %s\n", color.New(color.FgHiGreen).Sprint(agentYamlPath))
		return nil
	}

	if _, err := os.Stat(agentYmlPath); err == nil {
		p.agentDefinitionPath = agentYmlPath
		fmt.Printf("Using agent definition: %s\n", color.New(color.FgHiGreen).Sprint(agentYmlPath))
		return nil
	}

	return fmt.Errorf("agent definition file (agent.yaml or agent.yml) not found in %s. "+
		"Please ensure the file exists or set FOUNDRY_AGENT_DEFINITION_PATH environment variable", fullPath)
}

// Endpoints returns endpoints exposed by the agent service
func (p *AgentServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return []string{}, fmt.Errorf("not implemented")
}

// GetTargetResource returns a custom target resource for the agent service
func (p *AgentServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	return &azdext.TargetResource{}, nil
}

// Package performs packaging for the agent service
func (p *AgentServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	frameworkPackageOutput *azdext.ServicePackageResult,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{
		PackagePath:         frameworkPackageOutput.PackagePath,
		DockerPackageResult: &azdext.DockerPackageResult{},
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
	// Get env from azd
	azdEnvClient := p.azdClient.Environment()
	currEnv, err := azdEnvClient.GetCurrent(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get current environment: %w", err)
	}

	resp, err := azdEnvClient.GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: currEnv.Environment.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get environment values: %w", err)
	}
	azdEnv := make(map[string]string, len(resp.KeyValues))
	for _, kval := range resp.KeyValues {
		azdEnv[kval.Key] = kval.Value
	}

	// Reload the agent manifest from file to ensure we have the latest version
	data, err := os.ReadFile(p.agentDefinitionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file: %w", err)
	}

	agentManifest, err := agent_yaml.LoadAndValidateAgentManifest(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse and validate YAML: %w", err)
	}
	
	isHosted := false
	if agent_api.AgentKind(agentManifest.Agent.Kind) == agent_api.AgentKindHosted {
		isHosted = true
	}

	// *************************************Step 1: Build Agent Image (Hosted agents only)************
	var fullImageURL string
	if isHosted {
		var err error
		fullImageURL, err = p.buildAgentImage(ctx, agentManifest, azdEnv)
		if err != nil {
			return nil, fmt.Errorf("failed to build agent image: %w", err)
		}
	}

	// *************************************Step 2: Create Agent*************************************
	// Check if environment variable is set
	if azdEnv["AZURE_AI_PROJECT_ENDPOINT"] == "" {
		return nil, fmt.Errorf("AZURE_AI_PROJECT_ENDPOINT environment variable is required")
	}
	
	// Create Azure credential
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Loaded configuration from: %s\n", p.agentDefinitionPath)
	fmt.Fprintf(os.Stderr, "Using endpoint: %s\n", azdEnv["AZURE_AI_PROJECT_ENDPOINT"])
	fmt.Fprintf(os.Stderr, "Agent Name: %s\n", agentManifest.Agent.Name)

	// Option 1: Use the new structured approach (recommended)
	request, err := agent_yaml.CreateAgentAPIRequestFromManifest(*agentManifest, agent_yaml.WithImageURL(fullImageURL))
	if err != nil {
		return nil, fmt.Errorf("failed to create agent request: %w", err)
	}

	// Option 2: Use the two-step approach if you need the intermediate objects
	// definition, buildConfig, err := agent_yaml.BuildAgentDefinitionFromManifest(*agentManifest, agent_yaml.WithImageURL(fullImageURL))
	// if err != nil {
	//     return nil, fmt.Errorf("failed to build agent definition: %w", err)
	// }
	// request, err := agent_yaml.CreateAgentAPIRequestFromAgentDefinition(definition, buildConfig)
	// if err != nil {
	//     return nil, fmt.Errorf("failed to create agent request: %w", err)
	// }

	description := "No description"
	if request.Description != nil {
		desc := *request.Description
		if len(desc) > 50 {
			description = desc[:50] + "..."
		} else {
			description = desc
		}
	}
	fmt.Fprintf(os.Stderr, "Description: %s\n", description)

	// Display agent-specific information
	if promptDef, ok := request.Definition.(agent_api.PromptAgentDefinition); ok {
		fmt.Fprintf(os.Stderr, "Model: %s\n", promptDef.Model)
		instructions := "No instructions"
		if promptDef.Instructions != nil {
			inst := *promptDef.Instructions
			if len(inst) > 50 {
				instructions = inst[:50] + "..."
			} else {
				instructions = inst
			}
		}
		fmt.Fprintf(os.Stderr, "Instructions: %s\n", instructions)
	} else if imageHostedDef, ok := request.Definition.(agent_api.ImageBasedHostedAgentDefinition); ok {
		isHosted = true
		fmt.Fprintf(os.Stderr, "Image: %s\n", imageHostedDef.Image)
		fmt.Fprintf(os.Stderr, "CPU: %s\n", imageHostedDef.CPU)
		fmt.Fprintf(os.Stderr, "Memory: %s\n", imageHostedDef.Memory)
		fmt.Fprintf(os.Stderr, "Protocol Versions: %+v\n", imageHostedDef.ContainerProtocolVersions)
	}
	fmt.Fprintln(os.Stderr)

	// Create agent client
	agentClient := agent_api.NewAgentClient(azdEnv["AZURE_AI_PROJECT_ENDPOINT"], cred)
	// Use constant API version
	const apiVersion = "2025-05-15-preview"
	
	// Create agent
	agentResponse, err := agentClient.CreateAgent(ctx, request, apiVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Agent '%s' created successfully!\n", agentResponse.Name)

	// Register the agent name and version as azd environment variables
	_, err = azdEnvClient.SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: currEnv.Environment.Name,
		Key:     "AGENT_NAME",
		Value:   agentResponse.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set environment variable: %w", err)
	}

	_, err = azdEnvClient.SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: currEnv.Environment.Name,
		Key:     "AGENT_VERSION",
		Value:   agentResponse.Versions.Latest.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set environment variable: %w", err)
	}

	// *************************************Step 3: Start agent for HOBO (hosted agents only)************
	if !isHosted {
		// Return deployment result
		return &azdext.ServiceDeployResult{
			TargetResourceId: "",
			Kind:             "agent",
			Endpoints:        nil,
			Details: map[string]string{
				"message": "Agent service deployed successfully using custom extension logic",
			},
		}, nil
	}

	// Start the agent if it's a hosted agent
	fmt.Fprintln(os.Stderr, "Azure AI Agent Container Management Script")
	fmt.Fprintln(os.Stderr, "============================================")

	// Use constants for wait configuration
	const waitForReady = true
	const maxWaitTime = 10 * time.Minute
	
	// Extract replica configuration from agent manifest
	minReplicas := int32(1) // Default values
	maxReplicas := int32(1)
	
	// Check if the agent definition has scale configuration
	if containerAgent, ok := interface{}(agentManifest.Agent).(agent_yaml.ContainerAgent); ok {
		// For ContainerAgent, check if Options contains scale information
		if options, exists := containerAgent.Options["scale"]; exists {
			if scaleMap, ok := options.(map[string]interface{}); ok {
				if minReplicasFloat, exists := scaleMap["minReplicas"]; exists {
					if minReplicasVal, ok := minReplicasFloat.(float64); ok {
						minReplicas = int32(minReplicasVal)
					}
				}
				if maxReplicasFloat, exists := scaleMap["maxReplicas"]; exists {
					if maxReplicasVal, ok := maxReplicasFloat.(float64); ok {
						maxReplicas = int32(maxReplicasVal)
					}
				}
			}
		}
	}
	
	// Validate replica counts
	if minReplicas < 0 {
		return nil, fmt.Errorf("minReplicas must be non-negative, got: %d", minReplicas)
	}
	if maxReplicas < 0 {
		return nil, fmt.Errorf("maxReplicas must be non-negative, got: %d", maxReplicas)
	}
	if minReplicas > maxReplicas {
		return nil, fmt.Errorf("minReplicas (%d) cannot be greater than maxReplicas (%d)", minReplicas, maxReplicas)
	}

	fmt.Fprintf(os.Stderr, "Using endpoint: %s\n", azdEnv["AZURE_AI_PROJECT_ENDPOINT"])
	fmt.Fprintf(os.Stderr, "Agent Name: %s\n", agentResponse.Name)
	fmt.Fprintf(os.Stderr, "Agent Version: %s\n", agentResponse.Versions.Latest.Version)
	fmt.Fprintf(os.Stderr, "Min Replicas: %d\n", minReplicas)
	fmt.Fprintf(os.Stderr, "Max Replicas: %d\n", maxReplicas)
	fmt.Fprintf(os.Stderr, "Wait for Ready: %t\n", waitForReady)
	if waitForReady {
		fmt.Fprintf(os.Stderr, "Max Wait Time: %v\n", maxWaitTime)
	}
	fmt.Fprintln(os.Stderr)

	// Start agent container (minReplicas and maxReplicas are already int32)
	operation, err := agentClient.StartAgentContainer(
		ctx, agentResponse.Name, agentResponse.Versions.Latest.Version, &minReplicas, &maxReplicas, apiVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to start agent container: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Agent container start operation initiated successfully!\n")

	// Wait for operation to complete if requested
	if waitForReady {
		fmt.Fprintf(os.Stderr, "Waiting for operation to complete (timeout: %v)...\n", maxWaitTime)
		
		// Poll the operation status
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		
		timeout := time.After(maxWaitTime)
		
		for {
			select {
			case <-timeout:
				return nil, fmt.Errorf("timeout waiting for operation to complete after %v", maxWaitTime)
			case <-ticker.C:
				completedOperation, err := agentClient.GetAgentContainerOperation(
					ctx, agentResponse.Name, operation.Body.ID, apiVersion)
				if err != nil {
					return nil, fmt.Errorf("failed to get operation status: %w", err)
				}

				// Check if operation is complete
				if completedOperation.Status == "Succeeded" || completedOperation.Status == "Failed" {
					if completedOperation.Status == "Failed" {
						return nil, fmt.Errorf("operation failed: %s", completedOperation.Error)
					}
					
					if completedOperation.Container != nil {
						fmt.Fprintf(os.Stderr, "Agent container '%s' (version: %s) operation completed! Container status: %s\n",
							agentResponse.Name, agentResponse.Versions.Latest.Version, completedOperation.Container.Status)
					} else {
						fmt.Fprintf(
							os.Stderr,
							"Agent container '%s' (version: %s) operation completed successfully!\n",
							agentResponse.Name, agentResponse.Versions.Latest.Version)
					}
					goto operationComplete
				}
				
				fmt.Fprintf(os.Stderr, "Operation status: %s\n", completedOperation.Status)
			}
		}
		operationComplete:
	} else {
		fmt.Fprintf(
			os.Stderr,
			"Agent container '%s' (version: %s) start operation initiated (ID: %s).\n",
			agentResponse.Name, agentResponse.Versions.Latest.Version, operation.Body.ID)
	}

	return &azdext.ServiceDeployResult{
		TargetResourceId: "",
		Kind:             "agent",
		Endpoints:        nil,
		Details: map[string]string{
			"message": "Agent service deployed successfully using custom extension logic",
		},
	}, nil
}

// buildAgentImage builds a container image for hosted agents
func (p *AgentServiceTargetProvider) buildAgentImage(
	ctx context.Context, agentManifest *agent_yaml.AgentManifest, azdEnv map[string]string) (string, error) {
	registryEndpoint := azdEnv["AZURE_CONTAINER_REGISTRY_ENDPOINT"]
	if registryEndpoint == "" {
		return "", fmt.Errorf("AZURE_CONTAINER_REGISTRY_ENDPOINT environment variable is required")
	}

	// Find Dockerfile in the same directory as agent.yaml
	agentDir := filepath.Dir(p.agentDefinitionPath)
	dockerfilePath := filepath.Join(agentDir, "Dockerfile")

	// Check if Dockerfile exists
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		return "", fmt.Errorf("dockerfile not found in agent directory: %s", dockerfilePath)
	}

	fmt.Fprintf(os.Stderr, "Building image for agent: %s (ID: %s)\n", agentManifest.Agent.Name, agentManifest.Agent.Name)
	fmt.Fprintf(os.Stderr, "Using Dockerfile: %s\n", dockerfilePath)
	fmt.Fprintf(os.Stderr, "Using Azure Container Registry: %s\n", registryEndpoint)

	// Create Azure credential
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create Azure credential: %w", err)
	}

	// Create build context (tar archive)
	buildContext, err := createBuildContext(dockerfilePath)
	if err != nil {
		return "", fmt.Errorf("failed to create build context: %w", err)
	}

	// Generate image names with custom version (or timestamp) and latest tags
	// TODO: add support for custom version
	imageNames := GenerateImageNamesFromAgent(agentManifest.Agent.Name, "")

	fmt.Fprintf(os.Stderr, "Starting remote build for images: %v\n", imageNames)

	// Start the build
	runID, err := startRemoteBuildWithAPI(ctx, cred, registryEndpoint, buildContext, imageNames, dockerfilePath, azdEnv)
	if err != nil {
		return "", fmt.Errorf("failed to start remote build: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Build started with run ID: %s\n", runID)

	// Monitor the build status with log streaming
	err = monitorBuildWithLogs(ctx, cred, registryEndpoint, runID, azdEnv)
	if err != nil {
		return "", fmt.Errorf("build monitoring failed: %w", err)
	}

	// Output the full image URL (use the first image name which has the version tag)
	registryHost := strings.TrimPrefix(registryEndpoint, "https://")
	fullImageURL := fmt.Sprintf("%s/%s", registryHost, imageNames[0])

	fmt.Fprintf(os.Stderr, "Build completed successfully!\n")
	fmt.Fprintf(os.Stderr, "Built image: %s\n", fullImageURL)

	// Output just the image URL to stdout for script consumption
	fmt.Print(fullImageURL)

	return fullImageURL, nil
}
