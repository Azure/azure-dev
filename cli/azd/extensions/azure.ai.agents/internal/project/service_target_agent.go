// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/braydonk/yaml"
	"github.com/drone/envsubst"
	"github.com/fatih/color"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
)

// Reference implementation

// agentAPIVersion is the API version used for agent endpoint invocation URLs.
const agentAPIVersion = "2025-11-15-preview"

// displayableProtocolEntry defines a protocol that produces user-visible invocation endpoints.
type displayableProtocolEntry struct {
	Protocol  agent_api.AgentProtocol
	URLPath   string // path suffix in the invocation URL
	EnvSuffix string // suffix used in AGENT_{KEY}_{SUFFIX}_ENDPOINT env vars
}

// displayableProtocols is the single source of truth for protocols that produce
// user-facing invocation endpoints and env vars.
var displayableProtocols = []displayableProtocolEntry{
	{Protocol: agent_api.AgentProtocolResponses, URLPath: "openai/responses", EnvSuffix: "RESPONSES"},
	{Protocol: agent_api.AgentProtocolInvocations, URLPath: "invocations", EnvSuffix: "INVOCATIONS"},
}

// ProtocolEnvSuffix pairs a user-facing label with the env var suffix
// used in AGENT_{KEY}_{SUFFIX}_ENDPOINT variables.
type ProtocolEnvSuffix struct {
	Label  string // e.g. "Responses"
	Suffix string // e.g. "RESPONSES"
}

// DisplayableProtocolEnvSuffixes returns the label/suffix pairs for all
// displayable protocols. This is the single source of truth shared by
// deployment (registerAgentEnvironmentVariables) and the show command.
func DisplayableProtocolEnvSuffixes() []ProtocolEnvSuffix {
	result := make([]ProtocolEnvSuffix, len(displayableProtocols))
	for i, dp := range displayableProtocols {
		result[i] = ProtocolEnvSuffix{
			Label:  string(dp.Protocol),
			Suffix: dp.EnvSuffix,
		}
	}
	return result
}

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

const (
	preBuiltImageArtifactSourceKey = "azure.ai.agents.imageSource"
	preBuiltImageArtifactSource    = "agent.yaml"
)

// containerImageRefRe is a basic pattern for container image references:
// [registry/]repository[:tag|@digest]
var containerImageRefRe = regexp.MustCompile(
	`^[a-zA-Z0-9]([a-zA-Z0-9._-]*/)*[a-zA-Z0-9][a-zA-Z0-9._-]*(:[a-zA-Z0-9._-]+|@sha256:[0-9a-fA-F]{64})?$`,
)

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
			"run 'azd env get-values' to verify environment values, or initialize/project-bind "+
				"with 'azd ai agent init --project-id ...'",
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
		//nolint:gosec // env path is an explicit user override; existence check is intentional
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

	// Collect per-protocol endpoint env vars
	var endpoints []string
	for _, dp := range displayableProtocols {
		key := fmt.Sprintf("AGENT_%s_%s_ENDPOINT", serviceKey, dp.EnvSuffix)
		if val := azdEnv[key]; val != "" {
			endpoints = append(endpoints, val)
		}
	}

	if len(endpoints) == 0 {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAgentEnvVars,
			fmt.Sprintf("no agent endpoint variables found for service %s", serviceKey),
			"run 'azd deploy' to deploy the agent and set these variables",
		)
	}

	return endpoints, nil
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
	projectsClient, err := armcognitiveservices.NewProjectsClient(
		p.foundryProject.SubscriptionID, p.credential, azure.NewArmClientOptions())
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeCognitiveServicesClientFailed,
			fmt.Sprintf("failed to create Cognitive Services Projects client: %s", err))
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
	// Code deploy: ZIP the source directory
	if p.isCodeDeployAgent() {
		progress("Packaging code")
		zipPath, sha256Hex, err := p.packageCodeDeploy(serviceConfig)
		if err != nil {
			return nil, exterrors.Internal(exterrors.OpContainerPackage, fmt.Sprintf("code packaging failed: %s", err))
		}

		return &azdext.ServicePackageResult{
			Artifacts: []*azdext.Artifact{
				{
					Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ARCHIVE,
					Location:     zipPath,
					LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
					Metadata: map[string]string{
						"type":   "code-zip",
						"sha256": sha256Hex,
					},
				},
			},
		}, nil
	}

	agentDef, isContainerAgent, err := p.loadContainerAgentDefinition()
	if err != nil {
		return nil, err
	}
	if !isContainerAgent {
		return &azdext.ServicePackageResult{}, nil
	}

	usePreBuiltImage, err := p.shouldUsePreBuiltImage(ctx, agentDef)
	if err != nil {
		return nil, err
	}
	if usePreBuiltImage {
		progress("Using pre-built container image, skipping package")
		return &azdext.ServicePackageResult{
			Artifacts: []*azdext.Artifact{preBuiltImageArtifact(agentDef.Image)},
		}, nil
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
	// Code deploy skips Publish (no ACR needed)
	if p.isCodeDeployAgent() {
		return &azdext.ServicePublishResult{}, nil
	}

	if preBuiltArtifact := findPreBuiltImageArtifact(serviceContext.Package); preBuiltArtifact != nil {
		progress("Using pre-built container image, skipping publish")
		return &azdext.ServicePublishResult{
			Artifacts: []*azdext.Artifact{preBuiltArtifact},
		}, nil
	}

	_, isContainerAgent, err := p.loadContainerAgentDefinition()
	if err != nil {
		return nil, err
	}
	if !isContainerAgent {
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

func preBuiltImageArtifact(imageURL string) *azdext.Artifact {
	return &azdext.Artifact{
		Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
		Location:     imageURL,
		LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
		Metadata: map[string]string{
			preBuiltImageArtifactSourceKey: preBuiltImageArtifactSource,
		},
	}
}

func findPreBuiltImageArtifact(artifacts []*azdext.Artifact) *azdext.Artifact {
	for _, artifact := range artifacts {
		if artifact.Kind == azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER &&
			artifact.LocationKind == azdext.LocationKind_LOCATION_KIND_REMOTE &&
			artifact.Location != "" &&
			artifact.Metadata[preBuiltImageArtifactSourceKey] == preBuiltImageArtifactSource {
			return artifact
		}
	}

	return nil
}

func findPreBuiltImageArtifactInContext(serviceContext *azdext.ServiceContext) *azdext.Artifact {
	if serviceContext == nil {
		return nil
	}

	if artifact := findPreBuiltImageArtifact(serviceContext.Publish); artifact != nil {
		return artifact
	}

	return findPreBuiltImageArtifact(serviceContext.Package)
}

func hasContainerArtifact(artifacts []*azdext.Artifact) bool {
	for _, artifact := range artifacts {
		if artifact.Kind == azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER {
			return true
		}
	}

	return false
}

func (p *AgentServiceTargetProvider) loadContainerAgentDefinition() (agent_yaml.ContainerAgent, bool, error) {
	data, err := os.ReadFile(p.agentDefinitionPath)
	if err != nil {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to read agent manifest file: %s", err),
			"verify the agent.yaml file exists and is readable",
		)
	}

	if err := agent_yaml.ValidateAgentDefinition(data); err != nil {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent.yaml is not valid: %s", err),
			"fix the agent.yaml file according to the schema",
		)
	}

	var genericTemplate map[string]any
	if err := yaml.Unmarshal(data, &genericTemplate); err != nil {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("YAML content is not valid: %s", err),
			"verify the agent.yaml has valid YAML syntax",
		)
	}

	kind, ok := genericTemplate["kind"].(string)
	if !ok {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeMissingAgentKind,
			"kind field is missing or not a valid string in agent.yaml",
			"add a valid 'kind' field (e.g., 'hosted') to agent.yaml",
		)
	}

	if kind != string(agent_yaml.AgentKindHosted) {
		return agent_yaml.ContainerAgent{}, false, nil
	}

	var agentDef agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &agentDef); err != nil {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("YAML content is not valid for hosted agent: %s", err),
			"fix the agent.yaml to match the hosted agent schema",
		)
	}

	if agentDef.Image != "" && !containerImageRefRe.MatchString(agentDef.Image) {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("invalid container image reference in agent.yaml: %q", agentDef.Image),
			"use a valid image reference, e.g. 'myregistry.azurecr.io/image:v1'",
		)
	}

	return agentDef, true, nil
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

	warnDeprecatedScaleSettings(serviceConfig.Config)

	agentDef, isContainerAgent, err := p.loadContainerAgentDefinition()
	if err != nil {
		return nil, err
	}
	if !isContainerAgent {
		return nil, exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			"unsupported agent kind in agent.yaml",
			"use a supported kind: 'hosted'",
		)
	}

	// Branch: code deploy vs container deploy
	if agentDef.CodeConfiguration != nil {
		return p.deployHostedCodeAgent(ctx, serviceConfig, serviceContext, progress, agentDef, azdEnv)
	}

	return p.deployHostedAgent(ctx, serviceConfig, serviceContext, progress, agentDef, azdEnv)
}

// shouldUsePreBuiltImage determines whether to use a pre-built image.
//
// Behavior:
//   - If no image is configured in agent.yaml, always build from Dockerfile.
//   - In non-interactive mode (--no-prompt), the prompt returns the default
//     selection (index 0 = build from Dockerfile) automatically.
//   - In interactive mode, prompt the user. The default is to build, so users
//     who happen to have an image in agent.yaml are not silently switched onto
//     the pre-built path.
func (p *AgentServiceTargetProvider) shouldUsePreBuiltImage(
	ctx context.Context,
	agentDef agent_yaml.ContainerAgent,
) (bool, error) {
	imageURL := agentDef.Image
	if imageURL == "" {
		return false, nil
	}

	// Default to build so the pre-built path requires an explicit choice.
	// In non-interactive mode (--no-prompt), the framework returns the default
	// selection (index 0 = build) automatically.
	choices := []*azdext.SelectChoice{
		{Value: "build", Label: "Build a new image for me"},
		{Value: "prebuilt", Label: fmt.Sprintf("Create hosted agent from %s", imageURL)},
	}
	defaultIndex := int32(0)
	resp, err := p.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "A container image is configured. How would you like to deploy?",
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		return false, exterrors.FromPrompt(err, "failed to select hosted agent container image source")
	}

	return resp.Value != nil && choices[*resp.Value].Value == "prebuilt", nil
}

// isCodeDeployAgent returns true if the agent.yaml has code_configuration (code deploy mode)
func (p *AgentServiceTargetProvider) isCodeDeployAgent() bool {
	data, err := os.ReadFile(p.agentDefinitionPath)
	if err != nil {
		return false
	}

	var genericTemplate map[string]any
	if err := yaml.Unmarshal(data, &genericTemplate); err != nil {
		return false
	}

	kind, ok := genericTemplate["kind"].(string)
	if !ok {
		return false
	}

	if kind != string(agent_yaml.AgentKindHosted) {
		return false
	}

	_, hasCodeConfig := genericTemplate["code_configuration"]
	return hasCodeConfig
}

// deployPrepResult holds the common outputs from prepareDeploy, used by both
// container and code deploy paths.
type deployPrepResult struct {
	resolvedEnvVars map[string]string
	request         *agent_api.CreateAgentRequest
	protocols       []agent_yaml.ProtocolVersionRecord
}

// prepareDeploy handles the common pre-deploy logic shared by container and code
// deploy: endpoint validation, environment variable resolution, service config
// parsing, and API request building. The caller provides extra build options
// (e.g. WithImageURL for container, WithCPU/WithMemory for code).
func (p *AgentServiceTargetProvider) prepareDeploy(
	serviceConfig *azdext.ServiceConfig,
	agentDef agent_yaml.ContainerAgent,
	azdEnv map[string]string,
	extraOptions []agent_yaml.AgentBuildOption,
) (*deployPrepResult, error) {
	if azdEnv["AZURE_AI_PROJECT_ENDPOINT"] == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"AZURE_AI_PROJECT_ENDPOINT is required: environment variable was not found in the current azd environment",
			"run 'azd provision' or connect to an existing project via 'azd ai agent init --project-id <resource-id>'",
		)
	}

	fmt.Fprintf(os.Stderr, "Loaded configuration from: %s\n", p.agentDefinitionPath)
	fmt.Fprintf(os.Stderr, "Using endpoint: %s\n", azdEnv["AZURE_AI_PROJECT_ENDPOINT"])
	fmt.Fprintf(os.Stderr, "Agent Name: %s\n", agentDef.Name)

	// Resolve environment variables from YAML using azd environment values
	resolvedEnvVars := make(map[string]string)
	if agentDef.EnvironmentVariables != nil {
		for _, envVar := range *agentDef.EnvironmentVariables {
			resolvedEnvVars[envVar.Name] = p.resolveEnvironmentVariables(envVar.Value, azdEnv)
		}
	}

	// Parse service config for container resource overrides
	var foundryAgentConfig *ServiceTargetAgentConfig
	if err := UnmarshalStruct(serviceConfig.Config, &foundryAgentConfig); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to parse foundry agent config: %s", err),
			"check the service configuration in azure.yaml",
		)
	}

	warnDeprecatedScaleSettings(serviceConfig.Config)

	var cpu, memory string
	if foundryAgentConfig != nil && foundryAgentConfig.Container != nil && foundryAgentConfig.Container.Resources != nil {
		cpu = foundryAgentConfig.Container.Resources.Cpu
		memory = foundryAgentConfig.Container.Resources.Memory
	}

	// Build options: env vars + cpu/memory (if set) + caller-provided extras
	options := []agent_yaml.AgentBuildOption{
		agent_yaml.WithEnvironmentVariables(resolvedEnvVars),
	}
	if cpu != "" {
		options = append(options, agent_yaml.WithCPU(cpu))
	}
	if memory != "" {
		options = append(options, agent_yaml.WithMemory(memory))
	}
	options = append(options, extraOptions...)

	request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(agentDef, options...)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentRequest,
			fmt.Sprintf("failed to create agent request from definition: %s", err),
			"verify the agent.yaml definition is correct",
		)
	}

	applyAgentMetadata(request)

	// Default to "responses" protocol when none specified in agent.yaml.
	protocols := agentDef.Protocols
	if len(protocols) == 0 {
		protocols = []agent_yaml.ProtocolVersionRecord{
			{Protocol: string(agent_api.AgentProtocolResponses), Version: "1.0.0"},
		}
	}

	return &deployPrepResult{
		resolvedEnvVars: resolvedEnvVars,
		request:         request,
		protocols:       protocols,
	}, nil
}

// finalizeDeploy handles the common post-deploy logic: registering environment
// variables and building the deploy result artifacts.
func (p *AgentServiceTargetProvider) finalizeDeploy(
	ctx context.Context,
	progress azdext.ProgressReporter,
	serviceConfig *azdext.ServiceConfig,
	azdEnv map[string]string,
	agentVersion *agent_api.AgentVersionObject,
	protocols []agent_yaml.ProtocolVersionRecord,
) (*azdext.ServiceDeployResult, error) {
	progress("Registering agent environment variables")

	err := p.registerAgentEnvironmentVariables(ctx, azdEnv, serviceConfig, agentVersion, protocols)
	if err != nil {
		return nil, err
	}

	artifacts := p.deployArtifacts(
		agentVersion.Name,
		agentVersion.Version,
		azdEnv["AZURE_AI_PROJECT_ID"],
		azdEnv["AZURE_AI_PROJECT_ENDPOINT"],
		protocols,
	)

	return &azdext.ServiceDeployResult{
		Artifacts: artifacts,
	}, nil
}

// deployHostedAgent deploys a container-based hosted agent to the Foundry service.
func (p *AgentServiceTargetProvider) deployHostedAgent(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
	agentDef agent_yaml.ContainerAgent,
	azdEnv map[string]string,
) (*azdext.ServiceDeployResult, error) {
	progress("Deploying hosted agent")

	fullImageURL := ""
	if preBuiltArtifact := findPreBuiltImageArtifactInContext(serviceContext); preBuiltArtifact != nil {
		fullImageURL = preBuiltArtifact.Location
	} else if !hasContainerArtifact(serviceContext.Publish) {
		usePreBuiltImage, err := p.shouldUsePreBuiltImage(ctx, agentDef)
		if err != nil {
			return nil, err
		}
		if usePreBuiltImage {
			fullImageURL = agentDef.Image
		}
	}

	if fullImageURL != "" {
		progress(fmt.Sprintf("Using pre-built container image: %s", fullImageURL))
	} else {
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
				"published container artifact not found: no remote container artifact was found in service "+
					"publish artifacts and no pre-built image was specified",
				"either set 'image' in agent.yaml, "+
					"or run 'azd package' and 'azd publish' to build from a Dockerfile",
			)
		}
	}

	prep, err := p.prepareDeploy(serviceConfig, agentDef, azdEnv, []agent_yaml.AgentBuildOption{
		agent_yaml.WithImageURL(fullImageURL),
	})
	if err != nil {
		return nil, err
	}

	// Display agent information
	p.displayAgentInfo(prep.request)

	// Create agent
	progress("Creating agent")
	agentVersionResponse, err := p.createAgent(ctx, prep.request, azdEnv)
	if err != nil {
		return nil, err
	}

	return p.finalizeDeploy(ctx, progress, serviceConfig, azdEnv, agentVersionResponse, prep.protocols)
}

// packageCodeDeploy creates a ZIP archive of the agent source code, writes it to a temp file,
// and computes its SHA-256. Returns the temp file path and SHA-256 hex string.
func (p *AgentServiceTargetProvider) packageCodeDeploy(serviceConfig *azdext.ServiceConfig) (string, string, error) {
	// Source directory is the service's relative path
	srcDir := filepath.Dir(p.agentDefinitionPath)

	// Exclusion patterns
	excludeDirs := map[string]bool{
		"__pycache__":   true,
		".venv":         true,
		"venv":          true,
		".git":          true,
		"node_modules":  true,
		".mypy_cache":   true,
		".pytest_cache": true,
		".azure":        true,
	}
	excludeExts := map[string]bool{
		".pyc": true,
		".pyo": true,
	}
	excludeFiles := map[string]bool{
		".env": true,
	}

	// Create temp file and write ZIP directly to it while computing SHA-256
	tmpFile, err := os.CreateTemp("", "azd-code-deploy-*.zip")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp file for ZIP: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Clean up on error
	success := false
	defer func() {
		if !success {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	multiWriter := io.MultiWriter(tmpFile, hasher)
	zipWriter := zip.NewWriter(multiWriter)

	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// Skip root
		if relPath == "." {
			return nil
		}

		// Normalize to forward slashes for ZIP
		relPath = filepath.ToSlash(relPath)

		// Check directory exclusions
		if d.IsDir() {
			if excludeDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip symlinks to avoid including files outside the agent directory
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		// Check file extension exclusions
		if excludeExts[filepath.Ext(path)] {
			return nil
		}

		// Check file name exclusions (.env, .env.*)
		if excludeFiles[d.Name()] || strings.HasPrefix(d.Name(), ".env.") {
			return nil
		}

		// Skip agent.yaml itself from the ZIP (metadata is sent separately)
		if d.Name() == "agent.yaml" {
			return nil
		}

		// Add file to ZIP
		fileData, err := os.ReadFile(path) //nolint:gosec // path is constructed from filepath.WalkDir within the service directory
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", relPath, err)
		}

		writer, err := zipWriter.Create(relPath)
		if err != nil {
			return fmt.Errorf("failed to create ZIP entry %s: %w", relPath, err)
		}

		if _, err := writer.Write(fileData); err != nil {
			return fmt.Errorf("failed to write ZIP entry %s: %w", relPath, err)
		}

		return nil
	})

	if err != nil {
		return "", "", fmt.Errorf("failed to walk source directory: %w", err)
	}

	if err := zipWriter.Close(); err != nil {
		return "", "", fmt.Errorf("failed to close ZIP: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return "", "", fmt.Errorf("failed to close temp file: %w", err)
	}

	sha256Hex := hex.EncodeToString(hasher.Sum(nil))
	success = true

	return tmpPath, sha256Hex, nil
}

// deployHostedCodeAgent deploys a code-based hosted agent via multipart ZIP upload.
func (p *AgentServiceTargetProvider) deployHostedCodeAgent(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
	agentDef agent_yaml.ContainerAgent,
	azdEnv map[string]string,
) (*azdext.ServiceDeployResult, error) {
	progress("Deploying hosted agent (code deploy)")

	// Validate that the Foundry project's region supports code deploy.
	projectLocation := strings.ToLower(strings.TrimSpace(azdEnv["AZURE_LOCATION"]))
	if projectLocation == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeAgentCreateFailed,
			"AZURE_LOCATION is not set; the Foundry project region is required for code deploy",
			"run 'azd provision' or 'azd ai agent init' to set the project location",
		)
	}
	if !slices.Contains(CodeDeployRegions, projectLocation) {
		return nil, exterrors.Dependency(
			exterrors.CodeAgentCreateFailed,
			fmt.Sprintf(
				"code deploy is not supported in region %q; supported regions: %s",
				azdEnv["AZURE_LOCATION"],
				strings.Join(CodeDeployRegions, ", "),
			),
			"select a Foundry project in a supported region or use container deploy instead",
		)
	}

	// Find the ZIP artifact from Package phase
	var zipPath, sha256Hex string
	for _, artifact := range serviceContext.Package {
		if artifact.Metadata != nil && artifact.Metadata["type"] == "code-zip" {
			zipPath = artifact.Location
			sha256Hex = artifact.Metadata["sha256"]
			break
		}
	}
	if zipPath == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingCodeZipArtifact,
			"code ZIP artifact not found: no code-zip artifact was found in service package artifacts",
			"run 'azd package' to produce the code ZIP artifact",
		)
	}

	zipData, err := os.ReadFile(zipPath) //nolint:gosec // zipPath comes from the artifact location set during packaging
	if err != nil {
		return nil, fmt.Errorf("failed to read ZIP artifact: %w", err)
	}
	// Clean up temp file
	defer os.Remove(zipPath)

	prep, err := p.prepareDeploy(serviceConfig, agentDef, azdEnv, nil)
	if err != nil {
		return nil, err
	}

	if agentDef.CodeConfiguration != nil {
		fmt.Fprintf(os.Stderr, "Runtime: %s\n", agentDef.CodeConfiguration.Runtime)
		fmt.Fprintf(os.Stderr, "Entry Point: [\"python\", \"%s\"]\n", agentDef.CodeConfiguration.EntryPoint)
		depRes := "remote_build"
		if agentDef.CodeConfiguration.DependencyResolution != nil {
			depRes = *agentDef.CodeConfiguration.DependencyResolution
		}
		fmt.Fprintf(os.Stderr, "Packaging: %s\n", depRes)
	}

	// Display agent information
	p.displayAgentInfo(prep.request)

	// Build the metadata for multipart upload
	versionRequest := &agent_api.CreateAgentVersionRequest{
		Description: prep.request.Description,
		Metadata:    prep.request.Metadata,
		Definition:  prep.request.Definition,
	}

	// Create agent client
	agentClient := agent_api.NewAgentClient(
		azdEnv["AZURE_AI_PROJECT_ENDPOINT"],
		p.credential,
	)

	// Check if agent already exists (GET /agents/{name})
	progress("Creating agent")
	_, getErr := agentClient.GetAgent(ctx, agentDef.Name, agentAPIVersion)
	var agentResp *agent_api.AgentObject

	if getErr != nil {
		// Only fall back to create on 404; propagate other errors (auth, 5xx, network)
		if respErr, ok := errors.AsType[*azcore.ResponseError](getErr); !ok || respErr.StatusCode != http.StatusNotFound {
			return nil, fmt.Errorf("failed to check if agent exists: %w", getErr)
		}
		// Agent doesn't exist — create
		fmt.Fprintf(os.Stderr, "Creating new agent: %s\n", agentDef.Name)
		agentResp, err = agentClient.CreateAgentFromZip(ctx, agentDef.Name, versionRequest, zipData, sha256Hex, agentAPIVersion)
		if err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeAgentCreateFailed,
				fmt.Sprintf("failed to create agent from ZIP: %s; check the agent definition and try again", err),
			)
		}
	} else {
		// Agent exists — update
		fmt.Fprintf(os.Stderr, "Updating existing agent: %s\n", agentDef.Name)
		agentResp, err = agentClient.UpdateAgentFromZip(ctx, agentDef.Name, versionRequest, zipData, sha256Hex, agentAPIVersion)
		if err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeAgentCreateFailed,
				fmt.Sprintf("failed to update agent from ZIP: %s; check the agent definition and try again", err),
			)
		}
	}

	// Poll for status if remote build
	latestVersion := &agentResp.Versions.Latest
	depRes := "remote_build"
	if agentDef.CodeConfiguration != nil && agentDef.CodeConfiguration.DependencyResolution != nil {
		depRes = *agentDef.CodeConfiguration.DependencyResolution
	}
	if depRes == "remote_build" && latestVersion.Status == "creating" {
		fmt.Fprintf(os.Stderr, "Waiting for remote build to complete...\n")
		pollTimeout := 5 * time.Minute
		pollInterval := 5 * time.Second
		deadline := time.Now().Add(pollTimeout)

		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("deployment cancelled: %w", ctx.Err())
			case <-time.After(pollInterval):
			}
			versionResp, err := agentClient.GetAgentVersion(ctx, agentDef.Name, latestVersion.Version, agentAPIVersion)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: poll failed: %s\n", err)
				continue
			}
			latestVersion = versionResp
			if versionResp.Status == "active" {
				fmt.Fprintf(os.Stderr, "Agent is active!\n")
				break
			} else if versionResp.Status == "failed" {
				errMsg := "agent deployment failed during remote build; check agent logs or try local packaging (dependency_resolution: bundled)"
				if versionResp.Error != nil {
					errMsg = fmt.Sprintf("agent deployment failed: [%s] %s", versionResp.Error.Code, versionResp.Error.Message)
				}
				if versionResp.RequestID != "" {
					errMsg += fmt.Sprintf(" (request-id: %s)", versionResp.RequestID)
				}
				return nil, exterrors.Internal(
					exterrors.CodeAgentCreateFailed,
					errMsg,
				)
			}
			fmt.Fprintf(os.Stderr, "  Status: %s...\n", versionResp.Status)
		}

		if latestVersion.Status != "active" {
			return nil, exterrors.Internal(
				exterrors.CodeAgentCreateFailed,
				"agent deployment timed out waiting for remote build; check agent status manually or try local packaging",
			)
		}
	}

	// Patch agent-level fields (agent_endpoint, agent_card) if present.
	if prep.request.AgentEndpoint != nil || prep.request.AgentCard != nil {
		patchRequest := &agent_api.PatchAgentRequest{
			AgentEndpoint: prep.request.AgentEndpoint,
			AgentCard:     prep.request.AgentCard,
		}

		_, err := agentClient.PatchAgent(ctx, agentDef.Name, patchRequest, agentAPIVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"WARNING: Agent was created/updated, but patching agent endpoint/card failed: %s\n", err,
			)
			return nil, exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
		}
		fmt.Fprintf(os.Stderr, "Agent endpoint/card updated.\n")
	}

	return p.finalizeDeploy(ctx, progress, serviceConfig, azdEnv, latestVersion, prep.protocols)
}

// deployArtifacts constructs the artifacts list for deployment results.
// It produces one endpoint artifact per displayable protocol.
func (p *AgentServiceTargetProvider) deployArtifacts(
	agentName string,
	agentVersion string,
	projectResourceID string,
	projectEndpoint string,
	protocols []agent_yaml.ProtocolVersionRecord,
) []*azdext.Artifact {
	artifacts := []*azdext.Artifact{}

	// Add playground URL
	if projectResourceID != "" {
		playgroundUrl, err := AgentPlaygroundURL(projectResourceID, agentName, agentVersion)
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

	// Add agent endpoint(s) — one per displayable protocol
	if projectEndpoint != "" {
		endpoints := agentInvocationEndpoints(projectEndpoint, agentName, protocols)
		for _, ep := range endpoints {
			artifacts = append(artifacts, &azdext.Artifact{
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
				Location:     ep.URL,
				LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
				Metadata: map[string]string{
					"agentName":    agentName,
					"agentVersion": agentVersion,
					"label":        fmt.Sprintf("Agent endpoint (%s)", ep.Protocol),
					"clickable":    "false",
				},
			})
		}

		// Attach the informational note to the last endpoint only, to avoid repetition.
		if len(endpoints) > 0 {
			last := artifacts[len(artifacts)-1]
			last.Metadata["note"] = "For information on invoking the agent, see " + output.WithLinkFormat(
				"https://aka.ms/azd-agents-invoke")
		}
	}

	return artifacts
}

// protocolEndpointInfo holds a displayable protocol label and its invocation URL.
type protocolEndpointInfo struct {
	Protocol string
	URL      string
}

// protocolPath maps an agent protocol to its URL path suffix.
// Returns empty string for protocols that should not be displayed.
func protocolPath(protocol string) string {
	for _, dp := range displayableProtocols {
		if agent_api.AgentProtocol(protocol) == dp.Protocol {
			return dp.URLPath
		}
	}
	return ""
}

// agentInvocationEndpoints builds the list of displayable invocation endpoints
// from the agent's protocols.
func agentInvocationEndpoints(
	projectEndpoint string,
	agentName string,
	protocols []agent_yaml.ProtocolVersionRecord,
) []protocolEndpointInfo {
	var endpoints []protocolEndpointInfo
	for _, p := range protocols {
		path := protocolPath(p.Protocol)
		if path == "" {
			continue
		}
		endpoints = append(endpoints, protocolEndpointInfo{
			Protocol: p.Protocol,
			URL: fmt.Sprintf(
				"%s/agents/%s/endpoint/protocols/%s?api-version=%s",
				projectEndpoint, agentName, path, agentAPIVersion),
		})
	}
	return endpoints
}

// AgentPlaygroundURL constructs a URL to the agent playground in the Foundry portal.
// It parses the ARM resource ID to extract subscription, resource group, account, and project info.
func AgentPlaygroundURL(projectResourceID, agentName, agentVersion string) (string, error) {
	resourceId, err := arm.ParseResourceID(projectResourceID)
	if err != nil {
		return "", fmt.Errorf("failed to parse project resource ID: %w", err)
	}

	// Encode subscription ID as base64 without padding for URL
	subscriptionId := resourceId.SubscriptionID
	encodedSubscriptionId, err := encodeSubscriptionID(subscriptionId)
	if err != nil {
		return "", fmt.Errorf("failed to encode subscription ID: %w", err)
	}

	resourceGroup := resourceId.ResourceGroupName

	// Validate that the resource ID represents a Foundry project (has a parent account).
	// Account-level IDs (no /projects/ child) would produce malformed playground URLs.
	// For project-level IDs, Parent.Name is the account; for account-level IDs,
	// Parent.Name is the resource group — we distinguish by checking ResourceType.
	if resourceId.Parent == nil ||
		!strings.Contains(string(resourceId.ResourceType.Type), "/") {
		return "", fmt.Errorf(
			"resource ID does not represent a Foundry project (missing parent account): %s",
			projectResourceID,
		)
	}

	accountName := resourceId.Parent.Name
	projectName := resourceId.Name

	url := fmt.Sprintf(
		"https://ai.azure.com/nextgen/r/%s,%s,,%s,%s/build/agents/%s/build?version=%s",
		encodedSubscriptionId, resourceGroup, accountName, projectName,
		agentName, agentVersion,
	)
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

	// Extract CreateAgentVersionRequest from CreateAgentRequest
	versionRequest := &agent_api.CreateAgentVersionRequest{
		Description: request.Description,
		Metadata:    request.Metadata,
		Definition:  request.Definition,
	}

	// Create agent version
	agentVersionResponse, err := agentClient.CreateAgentVersion(ctx, request.Name, versionRequest, agentAPIVersion)
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
	}

	fmt.Fprintf(os.Stderr, "Agent version '%s' created successfully!\n", agentVersionResponse.Name)

	// Patch agent-level fields (agent_endpoint, agent_card) if present.
	// These are agent-level properties, not version-level, so they require
	// a separate PatchAgent call after version creation.
	if request.AgentEndpoint != nil || request.AgentCard != nil {
		patchRequest := &agent_api.PatchAgentRequest{
			AgentEndpoint: request.AgentEndpoint,
			AgentCard:     request.AgentCard,
		}

		_, err := agentClient.PatchAgent(
			ctx, request.Name, patchRequest, agentAPIVersion,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"WARNING: Agent version '%s' (version %s) was created, "+
					"but updating agent endpoint/card failed.\n",
				agentVersionResponse.Name,
				agentVersionResponse.Version,
			)
			return nil, exterrors.ServiceFromAzure(
				err, exterrors.OpCreateAgent,
			)
		}

		fmt.Fprintf(os.Stderr,
			"Agent endpoint and card updated successfully!\n",
		)
	}

	return agentVersionResponse, nil
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
	if hostedDef, ok := request.Definition.(agent_api.HostedAgentDefinition); ok {
		if hostedDef.Image != "" {
			fmt.Fprintf(os.Stderr, "Image: %s\n", hostedDef.Image)
		}
		fmt.Fprintf(os.Stderr, "CPU: %s\n", hostedDef.CPU)
		fmt.Fprintf(os.Stderr, "Memory: %s\n", hostedDef.Memory)
		fmt.Fprintf(os.Stderr, "Protocol Versions: %+v\n", hostedDef.ProtocolVersions)
	}
	fmt.Fprintln(os.Stderr)
}

// registerAgentEnvironmentVariables registers agent information as azd environment variables.
// Per-protocol endpoint vars are set (e.g. AGENT_{KEY}_RESPONSES_ENDPOINT).
// The base agent endpoint (AGENT_{KEY}_ENDPOINT) is set to <projectEndpoint>/agents/<agentName>
// for session management.
func (p *AgentServiceTargetProvider) registerAgentEnvironmentVariables(
	ctx context.Context,
	azdEnv map[string]string,
	serviceConfig *azdext.ServiceConfig,
	agentVersionResponse *agent_api.AgentVersionObject,
	protocols []agent_yaml.ProtocolVersionRecord,
) error {
	if agentVersionResponse.Name == "" {
		return fmt.Errorf("agent name is empty; cannot register environment variables")
	}
	if agentVersionResponse.Version == "" {
		return fmt.Errorf("agent version is empty; cannot register environment variables")
	}

	serviceKey := p.getServiceKey(serviceConfig.Name)
	envVars := map[string]string{
		fmt.Sprintf("AGENT_%s_NAME", serviceKey):    agentVersionResponse.Name,
		fmt.Sprintf("AGENT_%s_VERSION", serviceKey): agentVersionResponse.Version,
	}

	// Set the base agent endpoint used for session management (not protocol-specific).
	baseEndpointKey := fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey)
	projectEndpoint := strings.TrimRight(azdEnv["AZURE_AI_PROJECT_ENDPOINT"], "/")
	envVars[baseEndpointKey] = fmt.Sprintf(
		"%s/agents/%s/versions/%s", projectEndpoint, agentVersionResponse.Name, agentVersionResponse.Version,
	)

	endpoints := agentInvocationEndpoints(
		azdEnv["AZURE_AI_PROJECT_ENDPOINT"],
		agentVersionResponse.Name,
		protocols,
	)
	for _, ep := range endpoints {
		suffix := strings.ToUpper(ep.Protocol)
		key := fmt.Sprintf("AGENT_%s_%s_ENDPOINT", serviceKey, suffix)
		envVars[key] = ep.URL
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

// applyAgentMetadata sets the enableVnextExperience metadata on the request.
// The "enableVnextExperience" key is a server-side API contract.
func applyAgentMetadata(request *agent_api.CreateAgentRequest) {
	if request.Metadata == nil {
		request.Metadata = make(map[string]string)
	}
	request.Metadata["enableVnextExperience"] = "true"
}

// warnDeprecatedScaleSettings prints a user-visible warning if the raw service config
// contains a container.scale section, which is no longer supported.
func warnDeprecatedScaleSettings(config *structpb.Struct) {
	if config == nil {
		return
	}
	containerVal, ok := config.Fields["container"]
	if !ok || containerVal.GetStructValue() == nil {
		return
	}
	if _, hasScale := containerVal.GetStructValue().Fields["scale"]; hasScale {
		fmt.Printf("%s\n", output.WithWarningFormat(
			"WARNING: container.scale settings (minReplicas/maxReplicas) are no longer supported and will be ignored. "+
				"Remove the container.scale section from your azure.yaml service configuration.",
		))
	}
}
