// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
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
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	if !p.isContainerAgent() {
		return &azdext.ServicePackageResult{}, nil
	}

	var packageArtifact *azdext.Artifact
	var newArtifacts []*azdext.Artifact

	for _, artifact := range serviceContext.Package {
		if artifact.Kind == azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER {
			packageArtifact = artifact
			break
		}
	}

	if packageArtifact == nil {
		var buildArtifact *azdext.Artifact
		for _, artifact := range serviceContext.Build {
			if artifact.Kind == azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER {
				buildArtifact = artifact
				break
			}
		}

		if buildArtifact == nil {
			buildResponse, err := p.azdClient.
				Container().
				Build(ctx, &azdext.ContainerBuildRequest{
					ServiceName:    serviceConfig.Name,
					ServiceContext: serviceContext,
				})
			if err != nil {
				return nil, fmt.Errorf("failed building container: %w", err)
			}

			serviceContext.Build = append(serviceContext.Build, buildResponse.Result.Artifacts...)
		}

		packageResponse, err := p.azdClient.
			Container().
			Package(ctx, &azdext.ContainerPackageRequest{
				ServiceName:    serviceConfig.Name,
				ServiceContext: serviceContext,
			})
		if err != nil {
			return nil, fmt.Errorf("failed packaging container: %w", err)
		}

		newArtifacts = append(newArtifacts, packageResponse.Result.Artifacts...)
	}

	return &azdext.ServicePackageResult{
		Artifacts: newArtifacts,
	}, nil
}

// Publish performs the publish operation for the agent service
func (p *AgentServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	if !p.isContainerAgent() {
		return &azdext.ServicePublishResult{}, nil
	}

	publishResponse, err := p.azdClient.
		Container().
		Publish(ctx, &azdext.ContainerPublishRequest{
			ServiceName:    serviceConfig.Name,
			ServiceContext: serviceContext,
		})

	if err != nil {
		return nil, fmt.Errorf("failed publishing container: %w", err)
	}

	return &azdext.ServicePublishResult{
		Artifacts: publishResponse.Result.Artifacts,
	}, nil
}

// Deploy performs the deployment operation for the agent service
func (p *AgentServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	// Get environment variables from azd
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

	// Load and validate the agent manifest
	data, err := os.ReadFile(p.agentDefinitionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file: %w", err)
	}

	agentManifest, err := agent_yaml.LoadAndValidateAgentManifest(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse and validate YAML: %w", err)
	}

	if azdEnv["AI_FOUNDRY_PROJECT_RESOURCE_ID"] == "" {
		return nil, fmt.Errorf("AI_FOUNDRY_PROJECT_RESOURCE_ID environment variable is required")
	}

	parsedResource, err := arm.ParseResourceID(azdEnv["AI_FOUNDRY_PROJECT_RESOURCE_ID"])
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource ID: %w", err)
	}

	// Get the tenant ID
	tenantResponse, err := p.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: parsedResource.SubscriptionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant ID: %w", err)
	}

	// Create Azure credential
	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResponse.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	// Determine agent type and delegate to appropriate deployment method
	switch agent_api.AgentKind(agentManifest.Agent.Kind) {
	case agent_api.AgentKindPrompt:
		return p.deployPromptAgent(ctx, cred, agentManifest, azdEnv)
	case agent_api.AgentKindHosted:
		return p.deployHostedAgent(ctx, cred, serviceContext, progress, agentManifest, azdEnv)
	default:
		return nil, fmt.Errorf("unsupported agent kind: %s", agentManifest.Agent.Kind)
	}
}

func (p *AgentServiceTargetProvider) isContainerAgent() bool {
	// Load and validate the agent manifest
	data, err := os.ReadFile(p.agentDefinitionPath)
	if err != nil {
		return false
	}

	agentManifest, err := agent_yaml.LoadAndValidateAgentManifest(data)
	if err != nil {
		return false
	}

	return agentManifest.Agent.Kind == agent_yaml.AgentKind(agent_api.AgentKindHosted)
}

// deployPromptAgent handles deployment of prompt-based agents
func (p *AgentServiceTargetProvider) deployPromptAgent(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	agentManifest *agent_yaml.AgentManifest,
	azdEnv map[string]string,
) (*azdext.ServiceDeployResult, error) {
	// Check if environment variable is set
	if azdEnv["AZURE_AI_PROJECT_ENDPOINT"] == "" {
		return nil, fmt.Errorf("AZURE_AI_PROJECT_ENDPOINT environment variable is required")
	}

	fmt.Fprintf(os.Stderr, "Deploying Prompt Agent\n")
	fmt.Fprintf(os.Stderr, "======================\n")
	fmt.Fprintf(os.Stderr, "Loaded configuration from: %s\n", p.agentDefinitionPath)
	fmt.Fprintf(os.Stderr, "Using endpoint: %s\n", azdEnv["AZURE_AI_PROJECT_ENDPOINT"])
	fmt.Fprintf(os.Stderr, "Agent Name: %s\n", agentManifest.Agent.Name)

	// Create agent request (no image URL needed for prompt agents)
	request, err := agent_yaml.CreateAgentAPIRequestFromManifest(*agentManifest)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent request: %w", err)
	}

	// Display agent information
	p.displayAgentInfo(request)

	// Create and deploy agent
	agentVersionResponse, err := p.createAgent(ctx, request, azdEnv, cred)
	if err != nil {
		return nil, err
	}

	// Register agent info in environment
	err = p.registerAgentEnvironmentVariables(ctx, agentVersionResponse)
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "Prompt agent '%s' deployed successfully!\n", agentVersionResponse.Name)

	return &azdext.ServiceDeployResult{
		Artifacts: []*azdext.Artifact{
			{
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DEPLOYMENT,
				Location:     agentVersionResponse.ID,
				LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
				Metadata: map[string]string{
					"agentName":    agentVersionResponse.Name,
					"agentVersion": agentVersionResponse.Version,
				},
			},
		},
	}, nil
}

// deployHostedAgent handles deployment of hosted container agents
func (p *AgentServiceTargetProvider) deployHostedAgent(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
	agentManifest *agent_yaml.AgentManifest,
	azdEnv map[string]string,
) (*azdext.ServiceDeployResult, error) {
	// Check if environment variable is set
	if azdEnv["AZURE_AI_PROJECT_ENDPOINT"] == "" {
		return nil, fmt.Errorf("AZURE_AI_PROJECT_ENDPOINT environment variable is required")
	}

	progress("Deploying Hosted Agent")

	// Step 1: Build container image
	var fullImageURL string
	for _, artifact := range serviceContext.Publish {
		if artifact.Kind == azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER && artifact.LocationKind == azdext.LocationKind_LOCATION_KIND_REMOTE {
			fullImageURL = artifact.Location
			break
		}
	}
	if fullImageURL == "" {
		return nil, errors.New("published container artifact not found")
	}

	fmt.Fprintf(os.Stderr, "Loaded configuration from: %s\n", p.agentDefinitionPath)
	fmt.Fprintf(os.Stderr, "Using endpoint: %s\n", azdEnv["AZURE_AI_PROJECT_ENDPOINT"])
	fmt.Fprintf(os.Stderr, "Agent Name: %s\n", agentManifest.Agent.Name)

	// Step 2: Create agent request with image URL
	request, err := agent_yaml.CreateAgentAPIRequestFromManifest(*agentManifest, agent_yaml.WithImageURL(fullImageURL))
	if err != nil {
		return nil, fmt.Errorf("failed to create agent request: %w", err)
	}

	// Display agent information
	p.displayAgentInfo(request)

	// Step 3: Create agent
	agentVersionResponse, err := p.createAgent(ctx, request, azdEnv, cred)
	if err != nil {
		return nil, err
	}

	// Register agent info in environment
	err = p.registerAgentEnvironmentVariables(ctx, agentVersionResponse)
	if err != nil {
		return nil, err
	}

	// Step 4: Start agent container
	err = p.startAgentContainer(ctx, agentManifest, agentVersionResponse, azdEnv, cred)
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "Hosted agent '%s' deployed successfully!\n", agentVersionResponse.Name)

	return &azdext.ServiceDeployResult{
		Artifacts: []*azdext.Artifact{
			{
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DEPLOYMENT,
				Location:     agentVersionResponse.ID,
				LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
				Metadata: map[string]string{
					"agentName":    agentVersionResponse.Name,
					"agentVersion": agentVersionResponse.Version,
				},
			},
		},
	}, nil
}

// createAgent creates a new version of the agent using the API
func (p *AgentServiceTargetProvider) createAgent(
	ctx context.Context,
	request *agent_api.CreateAgentRequest,
	azdEnv map[string]string,
	cred *azidentity.AzureDeveloperCLICredential,
) (*agent_api.AgentVersionObject, error) {
	// Create agent client
	agentClient := agent_api.NewAgentClient(azdEnv["AZURE_AI_PROJECT_ENDPOINT"], cred)

	// Use constant API version
	const apiVersion = "2025-05-15-preview"

	// Extract CreateAgentVersionRequest from CreateAgentRequest
	versionRequest := &agent_api.CreateAgentVersionRequest{
		Description: request.Description,
		Metadata:    request.Metadata,
		Definition:  request.Definition,
	}

	// Create agent version
	agentVersionResponse, err := agentClient.CreateAgentVersion(ctx, request.Name, versionRequest, apiVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent version: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Agent version '%s' created successfully!\n", agentVersionResponse.Name)
	return agentVersionResponse, nil
}

// startAgentContainer starts the hosted agent container
func (p *AgentServiceTargetProvider) startAgentContainer(
	ctx context.Context,
	agentManifest *agent_yaml.AgentManifest,
	agentVersionResponse *agent_api.AgentVersionObject,
	azdEnv map[string]string,
	cred *azidentity.AzureDeveloperCLICredential,
) error {
	fmt.Fprintln(os.Stderr, "Starting Agent Container")
	fmt.Fprintln(os.Stderr, "=======================")

	// Use constants for wait configuration
	const waitForReady = true
	const maxWaitTime = 10 * time.Minute
	const apiVersion = "2025-05-15-preview"

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
		return fmt.Errorf("minReplicas must be non-negative, got: %d", minReplicas)
	}
	if maxReplicas < 0 {
		return fmt.Errorf("maxReplicas must be non-negative, got: %d", maxReplicas)
	}
	if minReplicas > maxReplicas {
		return fmt.Errorf("minReplicas (%d) cannot be greater than maxReplicas (%d)", minReplicas, maxReplicas)
	}

	fmt.Fprintf(os.Stderr, "Using endpoint: %s\n", azdEnv["AZURE_AI_PROJECT_ENDPOINT"])
	fmt.Fprintf(os.Stderr, "Agent Name: %s\n", agentVersionResponse.Name)
	fmt.Fprintf(os.Stderr, "Agent Version: %s\n", agentVersionResponse.Version)
	fmt.Fprintf(os.Stderr, "Min Replicas: %d\n", minReplicas)
	fmt.Fprintf(os.Stderr, "Max Replicas: %d\n", maxReplicas)
	fmt.Fprintf(os.Stderr, "Wait for Ready: %t\n", waitForReady)
	if waitForReady {
		fmt.Fprintf(os.Stderr, "Max Wait Time: %v\n", maxWaitTime)
	}
	fmt.Fprintln(os.Stderr)

	// Create agent client
	agentClient := agent_api.NewAgentClient(azdEnv["AZURE_AI_PROJECT_ENDPOINT"], cred)

	// Start agent container (minReplicas and maxReplicas are already int32)
	operation, err := agentClient.StartAgentContainer(
		ctx, agentVersionResponse.Name, agentVersionResponse.Version, &minReplicas, &maxReplicas, apiVersion)
	if err != nil {
		return fmt.Errorf("failed to start agent container: %w", err)
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
				return fmt.Errorf("timeout waiting for operation to complete after %v", maxWaitTime)
			case <-ticker.C:
				completedOperation, err := agentClient.GetAgentContainerOperation(
					ctx, agentVersionResponse.Name, operation.Body.ID, apiVersion)
				if err != nil {
					return fmt.Errorf("failed to get operation status: %w", err)
				}

				// Check if operation is complete
				if completedOperation.Status == "Succeeded" || completedOperation.Status == "Failed" {
					if completedOperation.Status == "Failed" {
						return fmt.Errorf("operation failed: %s", completedOperation.Error)
					}

					if completedOperation.Container != nil {
						fmt.Fprintf(os.Stderr, "Agent container '%s' (version: %s) operation completed! Container status: %s\n",
							agentVersionResponse.Name, agentVersionResponse.Version, completedOperation.Container.Status)
					} else {
						fmt.Fprintf(
							os.Stderr,
							"Agent container '%s' (version: %s) operation completed successfully!\n",
							agentVersionResponse.Name, agentVersionResponse.Version)
					}
					return nil
				}

				fmt.Fprintf(os.Stderr, "Operation status: %s\n", completedOperation.Status)
			}
		}
	} else {
		fmt.Fprintf(
			os.Stderr,
			"Agent container '%s' (version: %s) start operation initiated (ID: %s).\n",
			agentVersionResponse.Name, agentVersionResponse.Version, operation.Body.ID)
	}

	return nil
}

// displayAgentInfo displays information about the agent being deployed
func (p *AgentServiceTargetProvider) displayAgentInfo(request *agent_api.CreateAgentRequest) {
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
		fmt.Fprintf(os.Stderr, "Image: %s\n", imageHostedDef.Image)
		fmt.Fprintf(os.Stderr, "CPU: %s\n", imageHostedDef.CPU)
		fmt.Fprintf(os.Stderr, "Memory: %s\n", imageHostedDef.Memory)
		fmt.Fprintf(os.Stderr, "Protocol Versions: %+v\n", imageHostedDef.ContainerProtocolVersions)
	}
	fmt.Fprintln(os.Stderr)
}

// registerAgentEnvironmentVariables registers agent information as azd environment variables
func (p *AgentServiceTargetProvider) registerAgentEnvironmentVariables(
	ctx context.Context,
	agentVersionResponse *agent_api.AgentVersionObject,
) error {
	azdEnvClient := p.azdClient.Environment()
	currEnv, err := azdEnvClient.GetCurrent(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	// Register the agent name and version as azd environment variables
	_, err = azdEnvClient.SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: currEnv.Environment.Name,
		Key:     "AGENT_NAME",
		Value:   agentVersionResponse.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to set AGENT_NAME environment variable: %w", err)
	}

	_, err = azdEnvClient.SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: currEnv.Environment.Name,
		Key:     "AGENT_VERSION",
		Value:   agentVersionResponse.Version,
	})
	if err != nil {
		return fmt.Errorf("failed to set AGENT_VERSION environment variable: %w", err)
	}

	return nil
}
