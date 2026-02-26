// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/braydonk/yaml"
	"github.com/drone/envsubst"
	"github.com/fatih/color"
	"github.com/google/uuid"
)

// Reference implementation

// Ensure AgentServiceTargetProvider implements ServiceTargetProvider interface
var _ azdext.ServiceTargetProvider = &AgentServiceTargetProvider{}

// AgentServiceTargetProvider is a minimal implementation of ServiceTargetProvider for demonstration
type AgentServiceTargetProvider struct {
	azdClient           *azdext.AzdClient
	serviceConfig       *azdext.ServiceConfig
	agentDefinitionPath string
	credential          *azidentity.AzureDeveloperCLICredential
	tenantId            string
	env                 *azdext.Environment
	foundryProject      *arm.ResourceID
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
		return exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			fmt.Sprintf("failed to get project: %s", err),
			"run 'azd init' to initialize your project",
		)
	}
	servicePath := serviceConfig.RelativePath
	fullPath := filepath.Join(proj.Project.Path, servicePath)

	// Get and store environment
	azdEnvClient := p.azdClient.Environment()
	currEnv, err := azdEnvClient.GetCurrent(ctx, nil)
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeEnvironmentNotFound,
			fmt.Sprintf("failed to get current environment: %s", err),
			"run 'azd env new' to create an environment",
		)
	}
	p.env = currEnv.Environment

	// Get subscription ID from environment
	resp, err := azdEnvClient.GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: p.env.Name,
		Key:     "AZURE_SUBSCRIPTION_ID",
	})
	if err != nil {
		return fmt.Errorf("failed to get AZURE_SUBSCRIPTION_ID: %w", err)
	}

	subscriptionId := resp.Value
	if subscriptionId == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAzureSubscription,
			"AZURE_SUBSCRIPTION_ID is required: environment variable was not found in the current azd environment",
			"run 'azd env get-values' to verify environment values, or initialize/project-bind with 'azd ai agent init --project-id ...'",
		)
	}

	// Get the tenant ID
	tenantResponse, err := p.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscriptionId,
	})
	if err != nil {
		return exterrors.Auth(
			exterrors.CodeTenantLookupFailed,
			fmt.Sprintf("failed to get tenant ID for subscription %s: %s", subscriptionId, err),
			"verify your Azure login with 'azd auth login' and that you have access to this subscription",
		)
	}
	p.tenantId = tenantResponse.TenantId

	// Create Azure credential
	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   p.tenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run 'azd auth login' to authenticate",
		)
	}
	p.credential = cred

	fmt.Fprintf(os.Stderr, "Project path: %s, Service path: %s\n", proj.Project.Path, fullPath)

	// Check if user has specified agent definition path via environment variable
	if envPath := os.Getenv("AGENT_DEFINITION_PATH"); envPath != "" {
		// Verify the file exists and has correct extension
		if _, err := os.Stat(envPath); os.IsNotExist(err) {
			return exterrors.Validation(
				exterrors.CodeAgentDefinitionNotFound,
				fmt.Sprintf("agent definition file specified in AGENT_DEFINITION_PATH does not exist: %s", envPath),
				"verify the path set in AGENT_DEFINITION_PATH points to a valid agent.yaml file",
			)
		}

		ext := strings.ToLower(filepath.Ext(envPath))
		if ext != ".yaml" && ext != ".yml" {
			return exterrors.Validation(
				exterrors.CodeAgentDefinitionNotFound,
				fmt.Sprintf("agent definition file must be a YAML file (.yaml or .yml), got: %s", envPath),
				"provide a file with .yaml or .yml extension",
			)
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

	return exterrors.Dependency(
		exterrors.CodeAgentDefinitionNotFound,
		fmt.Sprintf("agent definition file not found: no agent.yaml or agent.yml found in %s", fullPath),
		"add an agent.yaml/agent.yml file to the service directory or set AGENT_DEFINITION_PATH",
	)
}

// getServiceKey converts a service name into a standardized environment variable key format
func (p *AgentServiceTargetProvider) getServiceKey(serviceName string) string {
	serviceKey := strings.ReplaceAll(serviceName, " ", "_")
	serviceKey = strings.ReplaceAll(serviceKey, "-", "_")
	return strings.ToUpper(serviceKey)
}

// Endpoints returns endpoints exposed by the agent service
func (p *AgentServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	// Get all environment values
	resp, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: p.env.Name,
	})
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("failed to get environment values: %s", err),
			"run 'azd env get-values' to verify environment state",
		)
	}

	azdEnv := make(map[string]string, len(resp.KeyValues))
	for _, kval := range resp.KeyValues {
		azdEnv[kval.Key] = kval.Value
	}

	// Check if required environment variables are set
	if azdEnv["AZURE_AI_PROJECT_ENDPOINT"] == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"AZURE_AI_PROJECT_ENDPOINT is required: environment variable was not found in the current azd environment",
			"run 'azd provision' or connect to an existing project via 'azd ai agent init --project-id <resource-id>'",
		)
	}

	serviceKey := p.getServiceKey(serviceConfig.Name)
	agentNameKey := fmt.Sprintf("AGENT_%s_NAME", serviceKey)
	agentVersionKey := fmt.Sprintf("AGENT_%s_VERSION", serviceKey)

	if azdEnv[agentNameKey] == "" || azdEnv[agentVersionKey] == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAgentEnvVars,
			fmt.Sprintf("%s and %s environment variables are required", agentNameKey, agentVersionKey),
			"run 'azd deploy' to deploy the agent and set these variables",
		)
	}

	endpoint := p.agentEndpoint(azdEnv["AZURE_AI_PROJECT_ENDPOINT"], azdEnv[agentNameKey], azdEnv[agentVersionKey])

	return []string{endpoint}, nil
}

// GetTargetResource returns a custom target resource for the agent service
func (p *AgentServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	// Ensure Foundry project is loaded
	if err := p.ensureFoundryProject(ctx); err != nil {
		return nil, err
	}

	// Extract account name from parent resource ID
	if p.foundryProject.Parent == nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidFoundryResourceId,
			"invalid resource ID: missing parent account",
			"verify the AZURE_AI_PROJECT_ID is a valid Microsoft Foundry project resource ID",
		)
	}

	accountName := p.foundryProject.Parent.Name
	projectName := p.foundryProject.Name

	// Create Cognitive Services Projects client
	projectsClient, err := armcognitiveservices.NewProjectsClient(p.foundryProject.SubscriptionID, p.credential, azure.NewArmClientOptions())
	if err != nil {
		return nil, exterrors.Internal(exterrors.CodeCognitiveServicesClientFailed, fmt.Sprintf("failed to create Cognitive Services Projects client: %s", err))
	}

	// Get the Microsoft Foundry project
	projectResp, err := projectsClient.Get(ctx, p.foundryProject.ResourceGroupName, accountName, projectName, nil)
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpGetFoundryProject)
	}

	// Construct the target resource
	targetResource := &azdext.TargetResource{
		SubscriptionId:    p.foundryProject.SubscriptionID,
		ResourceGroupName: p.foundryProject.ResourceGroupName,
		ResourceName:      projectName,
		ResourceType:      "Microsoft.CognitiveServices/accounts/projects",
		Metadata: map[string]string{
			"accountName": accountName,
			"projectName": projectName,
		},
	}

	// Add location if available
	if projectResp.Location != nil {
		targetResource.Metadata["location"] = *projectResp.Location
	}

	return targetResource, nil
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

	progress("Packaging container")
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
				return nil, exterrors.Internal(exterrors.OpContainerBuild, fmt.Sprintf("container build failed: %s", err))
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
			return nil, exterrors.Internal(exterrors.OpContainerPackage, fmt.Sprintf("container package failed: %s", err))
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

	progress("Publishing container")
	publishResponse, err := p.azdClient.
		Container().
		Publish(ctx, &azdext.ContainerPublishRequest{
			ServiceName:    serviceConfig.Name,
			ServiceContext: serviceContext,
		})

	if err != nil {
		return nil, exterrors.Internal(exterrors.OpContainerPublish, fmt.Sprintf("container publish failed: %s", err))
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
	// Ensure Foundry project is loaded
	if err := p.ensureFoundryProject(ctx); err != nil {
		return nil, err
	}

	// Get environment variables from azd
	resp, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: p.env.Name,
	})
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("failed to get environment values: %s", err),
			"run 'azd env get-values' to verify environment state",
		)
	}

	azdEnv := make(map[string]string, len(resp.KeyValues))
	for _, kval := range resp.KeyValues {
		azdEnv[kval.Key] = kval.Value
	}

	var serviceTargetConfig *ServiceTargetAgentConfig
	if err := UnmarshalStruct(serviceConfig.Config, &serviceTargetConfig); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("failed to parse service target config: %s", err),
			"check the service configuration in azure.yaml",
		)
	}

	if serviceTargetConfig != nil {
		fmt.Println("Loaded custom service target configuration")
	}

	// Load and validate the agent manifest
	data, err := os.ReadFile(p.agentDefinitionPath)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to read agent manifest file: %s", err),
			"verify the agent.yaml file exists and is readable",
		)
	}

	err = agent_yaml.ValidateAgentDefinition(data)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent.yaml is not valid: %s", err),
			"fix the agent.yaml file according to the schema",
		)
	}

	var genericTemplate map[string]interface{}
	if err := yaml.Unmarshal(data, &genericTemplate); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("YAML content is not valid for deploy: %s", err),
			"verify the agent.yaml has valid YAML syntax",
		)
	}

	kind, ok := genericTemplate["kind"].(string)
	if !ok {
		return nil, exterrors.Validation(
			exterrors.CodeMissingAgentKind,
			"kind field is missing or not a valid string in agent.yaml",
			"add a valid 'kind' field (e.g., 'prompt' or 'hosted') to agent.yaml",
		)
	}

	switch kind {
	case string(agent_yaml.AgentKindPrompt):
		var agentDef agent_yaml.PromptAgent
		if err := yaml.Unmarshal(data, &agentDef); err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("YAML content is not valid for prompt agent deploy: %s", err),
				"fix the agent.yaml to match the prompt agent schema",
			)
		}
		return p.deployPromptAgent(ctx, serviceConfig, agentDef, azdEnv)
	case string(agent_yaml.AgentKindHosted):
		var agentDef agent_yaml.ContainerAgent
		if err := yaml.Unmarshal(data, &agentDef); err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("YAML content is not valid for hosted agent deploy: %s", err),
				"fix the agent.yaml to match the hosted agent schema",
			)
		}
		return p.deployHostedAgent(ctx, serviceConfig, serviceContext, progress, agentDef, azdEnv)
	default:
		return nil, exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			fmt.Sprintf("unsupported agent kind: %s", kind),
			"use a supported kind: 'prompt' or 'hosted'",
		)
	}
}

func (p *AgentServiceTargetProvider) isContainerAgent() bool {
	// Load and validate the agent manifest
	data, err := os.ReadFile(p.agentDefinitionPath)
	if err != nil {
		return false
	}

	err = agent_yaml.ValidateAgentDefinition(data)
	if err != nil {
		return false
	}

	var genericTemplate map[string]interface{}
	if err := yaml.Unmarshal(data, &genericTemplate); err != nil {
		return false
	}

	kind, ok := genericTemplate["kind"].(string)
	if !ok {
		return false
	}

	return kind == string(agent_yaml.AgentKindHosted)
}

// deployPromptAgent handles deployment of prompt-based agents
func (p *AgentServiceTargetProvider) deployPromptAgent(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	agentDef agent_yaml.PromptAgent,
	azdEnv map[string]string,
) (*azdext.ServiceDeployResult, error) {
	// Check if environment variable is set
	if azdEnv["AZURE_AI_PROJECT_ENDPOINT"] == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"AZURE_AI_PROJECT_ENDPOINT is required: environment variable was not found in the current azd environment",
			"run 'azd provision' or connect to an existing project via 'azd ai agent init --project-id <resource-id>'",
		)
	}

	fmt.Fprintf(os.Stderr, "Deploying Prompt Agent\n")
	fmt.Fprintf(os.Stderr, "======================\n")
	fmt.Fprintf(os.Stderr, "Loaded configuration from: %s\n", p.agentDefinitionPath)
	fmt.Fprintf(os.Stderr, "Using endpoint: %s\n", azdEnv["AZURE_AI_PROJECT_ENDPOINT"])
	fmt.Fprintf(os.Stderr, "Agent Name: %s\n", agentDef.Name)

	// Create agent request (no image URL needed for prompt agents)
	request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(agentDef)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentRequest,
			fmt.Sprintf("failed to create agent request from definition: %s", err),
			"verify the agent.yaml definition is correct",
		)
	}

	// Display agent information
	p.displayAgentInfo(request)

	// Create and deploy agent
	agentVersionResponse, err := p.createAgent(ctx, request, azdEnv)
	if err != nil {
		return nil, err
	}

	// Register agent info in environment
	err = p.registerAgentEnvironmentVariables(ctx, azdEnv, serviceConfig, agentVersionResponse)
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "Prompt agent '%s' deployed successfully!\n", agentVersionResponse.Name)

	artifacts := p.deployArtifacts(
		agentVersionResponse.Name,
		agentVersionResponse.Version,
		azdEnv["AZURE_AI_PROJECT_ID"],
		azdEnv["AZURE_AI_PROJECT_ENDPOINT"],
	)

	return &azdext.ServiceDeployResult{
		Artifacts: artifacts,
	}, nil
}

// deployHostedAgent handles deployment of hosted container agents
func (p *AgentServiceTargetProvider) deployHostedAgent(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
	agentDef agent_yaml.ContainerAgent,
	azdEnv map[string]string,
) (*azdext.ServiceDeployResult, error) {
	// Check if environment variable is set
	if azdEnv["AZURE_AI_PROJECT_ENDPOINT"] == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"AZURE_AI_PROJECT_ENDPOINT is required: environment variable was not found in the current azd environment",
			"run 'azd provision' or connect to an existing project via 'azd ai agent init --project-id <resource-id>'",
		)
	}

	progress("Deploying hosted agent")

	// Step 1: Build container image
	var fullImageURL string
	for _, artifact := range serviceContext.Publish {
		if artifact.Kind == azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER &&
			artifact.LocationKind == azdext.LocationKind_LOCATION_KIND_REMOTE {
			fullImageURL = artifact.Location
			break
		}
	}
	if fullImageURL == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingPublishedContainer,
			"published container artifact not found: no remote container artifact was found in service publish artifacts",
			"run 'azd package' and 'azd publish' (or 'azd deploy') to produce container artifacts",
		)
	}

	fmt.Fprintf(os.Stderr, "Loaded configuration from: %s\n", p.agentDefinitionPath)
	fmt.Fprintf(os.Stderr, "Using endpoint: %s\n", azdEnv["AZURE_AI_PROJECT_ENDPOINT"])
	fmt.Fprintf(os.Stderr, "Agent Name: %s\n", agentDef.Name)

	// Step 2: Resolve environment variables from YAML using azd environment values
	resolvedEnvVars := make(map[string]string)
	if agentDef.EnvironmentVariables != nil {
		for _, envVar := range *agentDef.EnvironmentVariables {
			resolvedEnvVars[envVar.Name] = p.resolveEnvironmentVariables(envVar.Value, azdEnv)
		}
	}

	// Step 3: Create agent request with image URL and resolved environment variables
	var foundryAgentConfig *ServiceTargetAgentConfig
	if err := UnmarshalStruct(serviceConfig.Config, &foundryAgentConfig); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to parse foundry agent config: %s", err),
			"check the service configuration in azure.yaml",
		)
	}

	var cpu, memory string
	if foundryAgentConfig.Container != nil && foundryAgentConfig.Container.Resources != nil {
		cpu = foundryAgentConfig.Container.Resources.Cpu
		memory = foundryAgentConfig.Container.Resources.Memory
	}

	// Build options list starting with required options
	options := []agent_yaml.AgentBuildOption{
		agent_yaml.WithImageURL(fullImageURL),
		agent_yaml.WithEnvironmentVariables(resolvedEnvVars),
	}

	// Conditionally add CPU and memory options if they're not empty
	if cpu != "" {
		options = append(options, agent_yaml.WithCPU(cpu))
	}
	if memory != "" {
		options = append(options, agent_yaml.WithMemory(memory))
	}

	request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(agentDef, options...)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentRequest,
			fmt.Sprintf("failed to create agent request from definition: %s", err),
			"verify the agent.yaml definition is correct",
		)
	}

	// Display agent information
	p.displayAgentInfo(request)

	// Step 4: Create agent
	progress("Creating agent")
	agentVersionResponse, err := p.createAgent(ctx, request, azdEnv)
	if err != nil {
		return nil, err
	}

	// Step 5: Start agent container
	progress("Starting agent container")
	err = p.startAgentContainer(ctx, foundryAgentConfig, agentVersionResponse, azdEnv)
	if err != nil {
		return nil, err
	}

	// Register agent info in environment
	progress("Registering agent environment variables")
	err = p.registerAgentEnvironmentVariables(ctx, azdEnv, serviceConfig, agentVersionResponse)
	if err != nil {
		return nil, err
	}

	artifacts := p.deployArtifacts(
		agentVersionResponse.Name,
		agentVersionResponse.Version,
		azdEnv["AZURE_AI_PROJECT_ID"],
		azdEnv["AZURE_AI_PROJECT_ENDPOINT"],
	)

	return &azdext.ServiceDeployResult{
		Artifacts: artifacts,
	}, nil
}

// deployArtifacts constructs the artifacts list for deployment results
func (p *AgentServiceTargetProvider) deployArtifacts(
	agentName string,
	agentVersion string,
	projectResourceID string,
	projectEndpoint string,
) []*azdext.Artifact {
	artifacts := []*azdext.Artifact{}

	// Add playground URL
	if projectResourceID != "" {
		playgroundUrl, err := p.agentPlaygroundUrl(projectResourceID, agentName, agentVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to generate agent playground link")
		} else if playgroundUrl != "" {
			artifacts = append(artifacts, &azdext.Artifact{
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
				Location:     playgroundUrl,
				LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
				Metadata: map[string]string{
					"label": "Agent playground (portal)",
				},
			})
		}
	}

	// Add agent endpoint
	if projectEndpoint != "" {
		agentEndpoint := p.agentEndpoint(projectEndpoint, agentName, agentVersion)
		artifacts = append(artifacts, &azdext.Artifact{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
			Location:     agentEndpoint,
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			Metadata: map[string]string{
				"agentName":    agentName,
				"agentVersion": agentVersion,
				"label":        "Agent endpoint",
				"clickable":    "false",
				"note":         "For information on invoking the agent, see " + output.WithLinkFormat("https://aka.ms/azd-agents-invoke"),
			},
		})
	}

	return artifacts
}

// agentEndpoint constructs the agent endpoint URL from the provided parameters
func (p *AgentServiceTargetProvider) agentEndpoint(projectEndpoint, agentName, agentVersion string) string {
	return fmt.Sprintf("%s/agents/%s/versions/%s", projectEndpoint, agentName, agentVersion)
}

// agentPlaygroundUrl constructs a URL to the agent playground in the Foundry portal
func (p *AgentServiceTargetProvider) agentPlaygroundUrl(projectResourceId, agentName, agentVersion string) (string, error) {
	resourceId, err := arm.ParseResourceID(projectResourceId)
	if err != nil {
		return "", err
	}

	// Encode subscription ID as base64 without padding for URL
	subscriptionId := resourceId.SubscriptionID
	encodedSubscriptionId, err := encodeSubscriptionID(subscriptionId)
	if err != nil {
		return "", fmt.Errorf("failed to encode subscription ID: %w", err)
	}

	resourceGroup := resourceId.ResourceGroupName
	if resourceId.Parent == nil {
		return "", fmt.Errorf("invalid Microsoft Foundry project ID: %s", projectResourceId)
	}

	accountName := resourceId.Parent.Name
	projectName := resourceId.Name

	url := fmt.Sprintf(
		"https://ai.azure.com/nextgen/r/%s,%s,,%s,%s/build/agents/%s/build?version=%s",
		encodedSubscriptionId, resourceGroup, accountName, projectName, agentName, agentVersion)
	return url, nil
}

// createAgent creates a new version of the agent using the API
func (p *AgentServiceTargetProvider) createAgent(
	ctx context.Context,
	request *agent_api.CreateAgentRequest,
	azdEnv map[string]string,
) (*agent_api.AgentVersionObject, error) {
	// Create agent client
	agentClient := agent_api.NewAgentClient(
		azdEnv["AZURE_AI_PROJECT_ENDPOINT"],
		p.credential,
	)

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
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
	}

	fmt.Fprintf(os.Stderr, "Agent version '%s' created successfully!\n", agentVersionResponse.Name)
	return agentVersionResponse, nil
}

// startAgentContainer starts the hosted agent container
func (p *AgentServiceTargetProvider) startAgentContainer(
	ctx context.Context,
	foundryAgentConfig *ServiceTargetAgentConfig,
	agentVersionResponse *agent_api.AgentVersionObject,
	azdEnv map[string]string,
) error {
	fmt.Fprintln(os.Stderr, "Starting Agent Container")
	fmt.Fprintln(os.Stderr, "=======================")

	// Use constants for wait configuration
	const waitForReady = true
	const maxWaitTime = 10 * time.Minute
	const apiVersion = "2025-05-15-preview"

	fmt.Fprintf(os.Stderr, "Using endpoint: %s\n", azdEnv["AZURE_AI_PROJECT_ENDPOINT"])
	fmt.Fprintf(os.Stderr, "Agent Name: %s\n", agentVersionResponse.Name)
	fmt.Fprintf(os.Stderr, "Agent Version: %s\n", agentVersionResponse.Version)
	fmt.Fprintf(os.Stderr, "Wait for Ready: %t\n", waitForReady)
	if waitForReady {
		fmt.Fprintf(os.Stderr, "Max Wait Time: %v\n", maxWaitTime)
	}
	fmt.Fprintln(os.Stderr)

	// Create agent client
	agentClient := agent_api.NewAgentClient(
		azdEnv["AZURE_AI_PROJECT_ENDPOINT"],
		p.credential,
	)

	var minReplicas, maxReplicas *int32
	if foundryAgentConfig.Container != nil && foundryAgentConfig.Container.Scale != nil {
		if foundryAgentConfig.Container.Scale.MinReplicas > 0 {
			minReplicasInt32 := int32(foundryAgentConfig.Container.Scale.MinReplicas)
			minReplicas = &minReplicasInt32
		}
		if foundryAgentConfig.Container.Scale.MaxReplicas > 0 {
			maxReplicasInt32 := int32(foundryAgentConfig.Container.Scale.MaxReplicas)
			maxReplicas = &maxReplicasInt32
		}
	}

	// Build StartAgentContainerOptions
	options := &agent_api.StartAgentContainerOptions{
		MinReplicas: minReplicas,
		MaxReplicas: maxReplicas,
	}

	// Start agent container
	operation, err := agentClient.StartAgentContainer(
		ctx, agentVersionResponse.Name, agentVersionResponse.Version, options, apiVersion)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpStartContainer)
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
				return exterrors.Internal(
					exterrors.CodeContainerStartTimeout,
					fmt.Sprintf(
						"timeout waiting for operation (id: %s) to complete after %v",
						operation.Body.ID,
						maxWaitTime,
					),
				)
			case <-ticker.C:
				completedOperation, err := agentClient.GetAgentContainerOperation(
					ctx, agentVersionResponse.Name, operation.Body.ID, apiVersion)
				if err != nil {
					return exterrors.ServiceFromAzure(err, exterrors.OpGetContainerOperation)
				}

				// Check if operation is complete
				if completedOperation.Status == "Failed" {
					// Try to get reason for failure by querying container API
					containerInfo, containerErr := agentClient.GetAgentContainer(
						ctx, agentVersionResponse.Name, agentVersionResponse.Version, apiVersion)
					if containerErr != nil {
						return exterrors.Internal(
							exterrors.CodeContainerStartFailed,
							fmt.Sprintf(
								"operation failed (id: %s): failed to retrieve container details: %s",
								operation.Body.ID,
								containerErr,
							),
						)
					}

					var errorMsg string
					if containerInfo.ErrorMessage != nil && *containerInfo.ErrorMessage != "" {
						errorMsg = fmt.Sprintf(
							"operation failed (id: %s): container status is %q with error: %s",
							operation.Body.ID,
							containerInfo.Status,
							*containerInfo.ErrorMessage,
						)
					} else {
						errorMsg = fmt.Sprintf("operation failed (id: %s): container status is %q with no error details", operation.Body.ID, containerInfo.Status)
					}

					return exterrors.Internal(exterrors.CodeContainerStartFailed, errorMsg)
				}

				if completedOperation.Status == "Succeeded" {
					if completedOperation.Container != nil {
						fmt.Fprintf(
							os.Stderr,
							"Agent container '%s' (version: %s) operation completed! Container status: %s\n",
							agentVersionResponse.Name,
							agentVersionResponse.Version,
							completedOperation.Container.Status,
						)
					} else {
						fmt.Fprintf(
							os.Stderr,
							"Agent container '%s' (version: %s) operation completed successfully!\n",
							agentVersionResponse.Name, agentVersionResponse.Version)
					}
					return nil
				}

				// Still in progress, continue polling
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
	azdEnv map[string]string,
	serviceConfig *azdext.ServiceConfig,
	agentVersionResponse *agent_api.AgentVersionObject,
) error {

	endpoint := p.agentEndpoint(
		azdEnv["AZURE_AI_PROJECT_ENDPOINT"],
		agentVersionResponse.Name,
		agentVersionResponse.Version,
	)

	serviceKey := p.getServiceKey(serviceConfig.Name)
	envVars := map[string]string{
		fmt.Sprintf("AGENT_%s_NAME", serviceKey):     agentVersionResponse.Name,
		fmt.Sprintf("AGENT_%s_VERSION", serviceKey):  agentVersionResponse.Version,
		fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey): endpoint,
	}

	for key, value := range envVars {
		_, err := p.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: p.env.Name,
			Key:     key,
			Value:   value,
		})
		if err != nil {
			return fmt.Errorf("failed to set environment variable %s: %w", key, err)
		}
	}

	return nil
}

// resolveEnvironmentVariables resolves ${ENV_VAR} style references in value using azd environment variables.
// Supports default values (e.g., "${VAR:-default}") and multiple expressions (e.g., "${VAR1}-${VAR2}").
func (p *AgentServiceTargetProvider) resolveEnvironmentVariables(value string, azdEnv map[string]string) string {
	resolved, err := envsubst.Eval(value, func(varName string) string {
		return azdEnv[varName]
	})
	if err != nil {
		// If resolution fails, return original value
		return value
	}
	return resolved
}

// ensureFoundryProject ensures the Foundry project resource ID is parsed and stored.
// Checks for AZURE_AI_PROJECT_ID environment variable.
func (p *AgentServiceTargetProvider) ensureFoundryProject(ctx context.Context) error {
	if p.foundryProject != nil {
		return nil
	}

	// Get all environment values
	resp, err := p.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: p.env.Name,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("failed to get AZURE_AI_PROJECT_ID: %s", err),
			"run 'azd env get-values' to verify environment state",
		)
	}

	// Check for Microsoft Foundry project resource ID (try both env var names)
	foundryResourceID := resp.Value
	if foundryResourceID == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAiProjectId,
			"Microsoft Foundry project ID is required: AZURE_AI_PROJECT_ID is not set",
			"run 'azd provision' or connect to an existing project via 'azd ai agent init --project-id <resource-id>'",
		)
	}

	// Parse the resource ID
	parsedResource, err := arm.ParseResourceID(foundryResourceID)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAiProjectId,
			fmt.Sprintf("failed to parse Microsoft Foundry project ID: %s", err),
			"verify the AZURE_AI_PROJECT_ID is a valid ARM resource ID",
		)
	}

	p.foundryProject = parsedResource
	return nil
}

// encodeSubscriptionID encodes a subscription ID GUID as base64 without padding
func encodeSubscriptionID(subscriptionID string) (string, error) {
	guid, err := uuid.Parse(subscriptionID)
	if err != nil {
		return "", fmt.Errorf("invalid subscription ID format: %w", err)
	}

	// Convert GUID to bytes (MarshalBinary never returns an error for uuid.UUID)
	guidBytes, _ := guid.MarshalBinary()

	// Encode as base64 and remove padding
	encoded := base64.URLEncoding.EncodeToString(guidBytes)
	return strings.TrimRight(encoded, "="), nil
}
