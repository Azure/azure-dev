// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/pkg/paths"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
)

// Reference implementation

// displayableProtocolEntry defines a protocol that produces user-visible invocation endpoints.
type displayableProtocolEntry struct {
	Protocol  agent_api.AgentProtocol
	URLPath   string // path suffix in the invocation URL (empty when BuildURL is set)
	EnvSuffix string // suffix used in AGENT_{KEY}_{SUFFIX}_ENDPOINT env vars
	// BuildURL optionally builds a custom invocation URL for this protocol.
	// When set, it overrides the generic URL template that uses URLPath.
	// projectEndpoint is the Foundry project root
	// (https://<account>.services.ai.azure.com/api/projects/<project>).
	BuildURL func(projectEndpoint, agentName string) string
}

// displayableProtocols is the single source of truth for protocols that produce
// user-facing invocation endpoints and env vars.
var displayableProtocols = []displayableProtocolEntry{
	{
		Protocol:  agent_api.AgentProtocolResponses,
		EnvSuffix: "RESPONSES",
		BuildURL:  buildResponsesProtocolURL,
	},
	{
		Protocol:  agent_api.AgentProtocolInvocations,
		EnvSuffix: "INVOCATIONS",
		BuildURL:  buildInvocationsProtocolURL,
	},
	{
		Protocol:  agent_api.AgentProtocolInvocationsWS,
		EnvSuffix: "INVOCATIONS_WS",
		BuildURL:  buildInvocationsWSProtocolURL,
	},
}

// buildResponsesProtocolURL builds the per-agent HTTPS URL for the "responses" protocol.
func buildResponsesProtocolURL(projectEndpoint, agentName string) string {
	return fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/openai/responses?api-version=%s",
		projectEndpoint, agentName, agent_api.AgentEndpointAPIVersion,
	)
}

// buildInvocationsProtocolURL builds the per-agent HTTPS URL for the "invocations" protocol.
func buildInvocationsProtocolURL(projectEndpoint, agentName string) string {
	return fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/invocations?api-version=%s",
		projectEndpoint, agentName, agent_api.AgentEndpointAPIVersion,
	)
}

// buildInvocationsWSProtocolURL builds the Foundry dispatcher-form WebSocket URL for the
// "invocations_ws" protocol. Unlike the per-agent HTTP protocols, invocations_ws uses a
// fixed data-plane route under /api/projects/agents/endpoint/protocols/invocations_ws and
// selects the agent via the project_name and agent_name query parameters. Callers must add
// their own agent_session_id query parameter when establishing a session.
//
// Returns "" if projectEndpoint cannot be parsed into a URL with a host: the dispatcher route
// requires both a host (for the wss:// authority) and a project name (derived from the URL
// path) to be callable, so emitting a partial URL would only register a non-callable endpoint.
// Callers (agentInvocationEndpoints) filter out empty results.
func buildInvocationsWSProtocolURL(projectEndpoint, agentName string) string {
	projectEndpoint = strings.TrimSpace(projectEndpoint)
	u, err := url.Parse(projectEndpoint)
	if err != nil || u.Host == "" {
		return ""
	}

	projectName := path.Base(u.Path)
	q := url.Values{}
	q.Set("api-version", agent_api.AgentEndpointAPIVersion)
	q.Set("project_name", projectName)
	q.Set("agent_name", agentName)
	return fmt.Sprintf(
		"wss://%s/api/projects/agents/endpoint/protocols/invocations_ws?%s",
		u.Host, q.Encode(),
	)
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
	projectPath         string
	servicePath         string
	// deployContextReady is set by every successful ensureDeployContext path;
	// agentDefinitionPath is only set for the file-based and env-override paths
	// (not the inline unified shape), so both are checked as the idempotency guard.
	deployContextReady bool
	credential         *azidentity.AzureDeveloperCLICredential
	tenantId           string
	env                *azdext.Environment
	foundryProject     *arm.ResourceID
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

// Initialize stores the service config. It is intentionally cheap: azd core
// calls it on every service-target for every action. Heavy work (resolving
// agent.yaml, tenant lookup, credential) lives in ensureDeployContext and runs
// only when a deploy-time entrypoint needs it.
func (p *AgentServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	p.serviceConfig = serviceConfig
	return nil
}

// ensureDeployContext lazily resolves the agent definition file, the azd
// environment, the tenant, and the credential. Idempotent via the
// agentDefinitionPath short-circuit.
func (p *AgentServiceTargetProvider) ensureDeployContext(ctx context.Context) error {
	if p.deployContextReady || p.agentDefinitionPath != "" {
		return nil
	}
	if p.serviceConfig == nil {
		return exterrors.Internal(
			exterrors.CodeInvalidServiceConfig,
			"service-target Initialize was not called before ensureDeployContext",
		)
	}

	proj, err := p.azdClient.Project().Get(ctx, nil)
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			fmt.Sprintf("failed to get project: %s", err),
			"run 'azd init' to initialize your project",
		)
	}
	servicePath := p.serviceConfig.RelativePath
	fullPath, err := paths.JoinAllowRoot(proj.Project.Path, servicePath)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("invalid service path for %s: %s", p.serviceConfig.Name, err),
			"update azure.yaml so the agent service path stays within the project directory",
		)
	}

	if err := p.ensureEnv(ctx); err != nil {
		return err
	}

	// Get subscription ID from environment
	azdEnvClient := p.azdClient.Environment()
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

	p.projectPath = proj.Project.Path
	p.servicePath = fullPath

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
		p.deployContextReady = true
		return nil
	}

	// Unified shape: the agent definition is carried inline on the service entry,
	// so no on-disk agent.yaml is required.
	if _, _, found, _, defErr := AgentDefinitionFromService(p.serviceConfig); defErr != nil {
		return defErr
	} else if found {
		p.deployContextReady = true
		return nil
	}

	// Legacy shape: look for agent.yaml or agent.yml in the service directory root
	agentYamlPath, err := paths.JoinAllowRoot(proj.Project.Path, servicePath, "agent.yaml")
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("invalid agent definition path for %s: %s", p.serviceConfig.Name, err),
			"update azure.yaml so the agent definition stays within the project directory",
		)
	}
	agentYmlPath, err := paths.JoinAllowRoot(proj.Project.Path, servicePath, "agent.yml")
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("invalid agent definition path for %s: %s", p.serviceConfig.Name, err),
			"update azure.yaml so the agent definition stays within the project directory",
		)
	}

	if _, err := os.Stat(agentYamlPath); err == nil {
		p.agentDefinitionPath = agentYamlPath
		fmt.Printf("Using agent definition: %s\n", color.New(color.FgHiGreen).Sprint(agentYamlPath))
		p.deployContextReady = true
		return nil
	}

	if _, err := os.Stat(agentYmlPath); err == nil {
		p.agentDefinitionPath = agentYmlPath
		fmt.Printf("Using agent definition: %s\n", color.New(color.FgHiGreen).Sprint(agentYmlPath))
		p.deployContextReady = true
		return nil
	}

	return exterrors.Dependency(
		exterrors.CodeAgentDefinitionNotFound,
		fmt.Sprintf("agent definition file not found: no agent.yaml or agent.yml found in %s", fullPath),
		"add an agent.yaml/agent.yml file to the service directory or set AGENT_DEFINITION_PATH",
	)
}

// ensureEnv lazily populates p.env from the azd host. Idempotent and cheap
// enough for non-deploy entrypoints (Endpoints, registerAgentEnvironmentVariables).
func (p *AgentServiceTargetProvider) ensureEnv(ctx context.Context) error {
	if p.env != nil {
		return nil
	}
	currEnv, err := p.azdClient.Environment().GetCurrent(ctx, nil)
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeEnvironmentNotFound,
			fmt.Sprintf("failed to get current environment: %s", err),
			"run 'azd env new' to create an environment",
		)
	}
	p.env = currEnv.Environment
	return nil
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
	if err := p.ensureEnv(ctx); err != nil {
		return nil, err
	}

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
	if azdEnv["FOUNDRY_PROJECT_ENDPOINT"] == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"FOUNDRY_PROJECT_ENDPOINT is required: environment variable was not found in the current azd environment",
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
	if err := p.ensureDeployContext(ctx); err != nil {
		return nil, err
	}
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
	if err := p.ensureDeployContext(ctx); err != nil {
		return nil, err
	}
	// Code deploy: ZIP the source directory
	if p.isCodeDeployAgent() {
		progress("Packaging code")
		zipPath, sha256Hex, err := p.packageCodeDeploy(ctx, serviceConfig)
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
	// Pre-built image: nothing to package or push. Skip deploy-context
	// resolution so this path stays cheap and doesn't require agent.yaml.
	if preBuiltArtifact := findPreBuiltImageArtifact(serviceContext.Package); preBuiltArtifact != nil {
		progress("Using pre-built container image, skipping publish")
		return &azdext.ServicePublishResult{
			Artifacts: []*azdext.Artifact{preBuiltArtifact},
		}, nil
	}

	if err := p.ensureDeployContext(ctx); err != nil {
		return nil, err
	}
	// Code deploy skips Publish (no ACR needed)
	if p.isCodeDeployAgent() {
		return &azdext.ServicePublishResult{}, nil
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
		return nil, classifyContainerPublishError(err)
	}

	return &azdext.ServicePublishResult{
		Artifacts: publishResponse.Result.Artifacts,
	}, nil
}

func classifyContainerPublishError(err error) error {
	if isPrivateACRNetworkAccessError(err) {
		return exterrors.Dependency(
			exterrors.CodePrivateACRNetworkAccessFailed,
			fmt.Sprintf(
				"container publish failed because the Azure Container Registry may be blocking network access: %s",
				err,
			),
			"allowlist the public outbound IP/CIDR of the dev environment running `azd deploy` in the ACR "+
				"firewall/network settings. If `docker.remoteBuild: true` is enabled, first set "+
				"`docker.remoteBuild: false` for this service because remote build worker IPs are not predictable. "+
				"Ensure Docker or Podman is installed and running, then run `azd deploy` again.",
		)
	}

	if isACRPermissionError(err) {
		return exterrors.Dependency(
			exterrors.CodeACRPermissionDenied,
			fmt.Sprintf(
				"container publish failed because your identity does not have permission to push "+
					"to the Azure Container Registry: %s",
				err,
			),
			acrPermissionSuggestionFor(err),
		)
	}

	if actionable := azdext.ActionableErrorDetailFromError(err); actionable != nil && actionable.GetSuggestion() != "" {
		return err
	}

	return exterrors.Internal(exterrors.OpContainerPublish, fmt.Sprintf("container publish failed: %s", err))
}

// acrPermissionSuggestionFor is the user-facing remediation text for
// CodeACRPermissionDenied. It offers a primary RBAC fix and an in-place
// fallback that switches the service to code (zip) deploy without re-running
// `azd ai agent init`.
//
// The recommended role depends on which API was denied:
//   - Remote-build path (docker.remoteBuild: true -- the new container deploy
//     default): the failing action is typically
//     Microsoft.ContainerRegistry/registries/listBuildSourceUploadUrl/action
//     or .../scheduleRun/action. AcrPush is data-plane only and does NOT grant
//     these; the correct role is "Container Registry Tasks Contributor".
//   - Local-push path (docker.remoteBuild: false): the failing action is the
//     docker push itself; AcrPush is sufficient.
//
// The emitted `az role assignment create` command uses the role definition
// GUID (not the display name) for the --role argument. GUIDs are guaranteed
// stable; display names could in principle be renamed by Azure. The human
// role name is still shown in the surrounding prose so the user understands
// what they are assigning.
//
// When the underlying error includes the principal's object id and/or the ACR
// resource scope (typical of ARM 403 responses), those values are substituted
// into the command so the user can paste it as-is. Otherwise placeholders
// are shown. ASCII-only per repo style.
func acrPermissionSuggestionFor(err error) string {
	assignee := "<your-object-id>"
	scope := "<acr-resource-id>"
	msgRaw := ""
	if err != nil {
		msgRaw = err.Error()
		if m := armObjectIDRe.FindStringSubmatch(msgRaw); len(m) == 2 {
			assignee = m[1]
		}
		if m := armACRScopeRe.FindStringSubmatch(msgRaw); len(m) == 2 {
			scope = m[1]
		}
	}

	isRemoteBuildPath := false
	if msgRaw != "" {
		lower := strings.ToLower(msgRaw)
		isRemoteBuildPath = containsAny(lower,
			"listbuildsourceuploadurl",
			"schedulerun",
			"remote build failed",
		)
	}

	// Role identifiers come from developer_rbac_check.go (same package).
	// Names are for prose; IDs are what the `az` command actually uses.
	primaryRoleName := "AcrPush"
	primaryRoleID := roleAcrPush
	pathContext := "data-plane push (used when docker.remoteBuild: false)"
	abacLine := fmt.Sprintf(
		"    - Container Registry Repository Writer   (role ID: %s)   for ABAC-mode registries",
		roleAcrRepositoryWriter,
	)
	if isRemoteBuildPath {
		primaryRoleName = "Container Registry Tasks Contributor"
		primaryRoleID = roleContainerRegistryTasksContributor
		pathContext = "ACR Tasks remote build (used when docker.remoteBuild: true)"
		// For Tasks-based builds on ABAC-mode registries, RepositoryWriter alone
		// does not cover Tasks actions. Owner / Contributor remain the broad
		// options.
		abacLine = "    - For ABAC-mode registries, an Owner or Contributor assignment may also be needed"
	}

	primaryLine := fmt.Sprintf("    - %s   (role ID: %s)", primaryRoleName, primaryRoleID)
	azCommand := fmt.Sprintf(
		`az role assignment create --assignee %s --role %s --scope %s`,
		assignee, primaryRoleID, scope,
	)

	return "Your identity needs permission to push container images to the Azure Container Registry.\n\n" +
		"This deployment failed on the " + pathContext + " path.\n\n" +
		"Recommended fix (keep container deploy):\n" +
		"  Ask a subscription Owner or User Access Administrator to assign one of these roles to your\n" +
		"  identity, then re-run `azd up`:\n" +
		primaryLine + "\n" +
		abacLine + "\n\n" +
		"  Example (run as a subscription Owner or User Access Administrator):\n" +
		"    " + azCommand + "\n\n" +
		"Alternative (switch this service to code (zip) deploy; no ACR push required):\n" +
		"  Code (zip) deploy uploads your source code directly to Foundry Agent Service.\n" +
		"  The service runs your agent in a Microsoft-managed platform container -- you do\n" +
		"  NOT need a Dockerfile or a custom container image. No container is built or\n" +
		"  pushed, so no ACR permissions are needed.\n\n" +
		"  Supported runtimes: python_3_13, python_3_14, dotnet_10\n\n" +
		"  Learn more: https://learn.microsoft.com/azure/foundry/agents/how-to/deploy-hosted-agent-code\n\n" +
		"  To switch (no need to re-run `azd ai agent init`):\n" +
		"  1. Open the service's agent.yaml and add a `code_configuration:` block under\n" +
		"     the hosted agent, for example:\n" +
		"        code_configuration:\n" +
		"          runtime: python_3_13          # or dotnet_10\n" +
		"          entry_point: app.py            # or MyAgent.dll\n" +
		"  2. Run: azd env set AZD_AGENT_SKIP_ACR true\n" +
		"     (subsequent provisioning will skip creating ACR; an already-provisioned\n" +
		"     ACR is not deleted automatically)\n" +
		"  3. Re-run: azd up"
}

func isPrivateACRNetworkAccessError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	hasACRContext := containsAny(message, acrContextSignals...)

	networkSignals := []string{
		"public network access",
		"private endpoint",
		"network rule",
		"firewall",
		"i/o timeout",
		"connection timed out",
		"tls handshake timeout",
		"connection refused",
		"no such host",
	}
	hasNetworkSignal := containsAny(message, networkSignals...)

	// Specific signal: ACR firewall block list. The "client with ip address ...
	// not allowed access" wording is unambiguous so we accept it standalone.
	if strings.Contains(message, "client with ip address") &&
		strings.Contains(message, "not allowed access") {
		return true
	}

	// Remote-build wrapper: require BOTH an explicit network signal AND ACR
	// context. The previous OR variant false-classified RBAC failures whose
	// only "signal" was the word "forbidden" -- those are now handled by
	// isACRPermissionError.
	if strings.Contains(message, "remote build failed") &&
		strings.Contains(message, "local fallback unavailable") {
		return hasNetworkSignal && hasACRContext
	}

	return hasACRContext && hasNetworkSignal
}

// isACRPermissionError reports whether err is an ACR push/build failure caused
// by missing RBAC or auth (as opposed to network access). Predicate is AND:
// the error must reference ACR (by login server, ARM resource type, or
// human-readable name) AND carry an explicit permission signal.
func isACRPermissionError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())

	if !containsAny(message, acrContextSignals...) {
		return false
	}

	permissionSignals := []string{
		"denied: requested access to the resource is denied",
		"unauthorized",
		"authentication required",
		"authorization failed",
		"authorizationfailed", // ARM ErrorCode (no space)
		"does not have authorization",
		"does not have rbac permission",
		"acrpush",
		"insufficient_scope",
		"repository access not allowed",
		"failed to fetch oauth token",
		"acr_token",
		"token exchange",
	}
	if containsAny(message, permissionSignals...) {
		return true
	}

	// Word-bounded 401/403 plus an explicit permission noun nearby. Avoid
	// bare "forbidden" / "token" matches that overlap with other failure modes.
	if has40xRe.MatchString(message) &&
		containsAny(message, "denied", "forbidden", "permission", "not authorized") {
		return true
	}

	return false
}

// acrContextSignals are substrings that indicate the error references an
// Azure Container Registry. ".azurecr.io" covers docker-push errors;
// "microsoft.containerregistry" covers ARM-side errors from the remote-build
// path (e.g. listBuildSourceUploadUrl/scheduleRun) which do NOT include the
// login server in the URL. The human-readable variants catch wrapper text.
var acrContextSignals = []string{
	".azurecr.io",
	"microsoft.containerregistry",
	"azure container registry",
	"container registry",
}

// has40xRe matches a bare 401 or 403 status code with word boundaries to avoid
// false positives on arbitrary digit runs.
var has40xRe = regexp.MustCompile(`(?i)\b40[13]\b`)

// armObjectIDRe extracts the principal object id from an ARM AuthorizationFailed
// error message of the form: ... with object id '<guid>' does not have authorization ...
var armObjectIDRe = regexp.MustCompile(`(?i)with object id '([0-9a-f-]{36})'`)

// armACRScopeRe extracts the ACR resource scope from an ARM AuthorizationFailed
// error message of the form: ... over scope '/subscriptions/.../Microsoft.ContainerRegistry/registries/<name>' ...
// Anchored to Microsoft.ContainerRegistry so we don't match unrelated scopes.
var armACRScopeRe = regexp.MustCompile(
	`(?i)over scope '(/subscriptions/[^']+/providers/Microsoft\.ContainerRegistry/registries/[^']+)'`,
)

func containsAny(s string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(s, value) {
			return true
		}
	}
	return false
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
	// An explicit AGENT_DEFINITION_PATH override is represented by
	// agentDefinitionPath and must win over the service entry.
	if p.agentDefinitionPath != "" {
		data, err := os.ReadFile(p.agentDefinitionPath)
		if err != nil {
			return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("failed to read agent manifest file: %s", err),
				"verify the agent.yaml file exists and is readable",
			)
		}

		WarnLegacyAgentShape(AgentDefinitionSourceDisk)
		return parseContainerAgentYAML(data)
	}

	// Prefer the agent definition carried inline on the service entry (the
	// unified service-level shape, or the deprecated config-nested shape).
	if ca, isHosted, found, source, err := AgentDefinitionFromService(p.serviceConfig); found || err != nil {
		if found && source.IsLegacy() {
			WarnLegacyAgentShape(source)
		}
		return ca, isHosted, err
	}

	return agent_yaml.ContainerAgent{}, false, exterrors.Dependency(
		exterrors.CodeAgentDefinitionNotFound,
		fmt.Sprintf("agent definition not found for service %q", p.serviceConfig.GetName()),
		"re-run `azd ai agent init` to write the agent definition into azure.yaml",
	)
}

// Deploy performs the deployment operation for the agent service
func (p *AgentServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	if err := p.ensureDeployContext(ctx); err != nil {
		return nil, err
	}
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

	serviceTargetConfig, err := LoadServiceTargetAgentConfig(serviceConfig)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("failed to parse service target config: %s", err),
			"check the service configuration in azure.yaml",
		)
	}

	if serviceTargetConfig != nil {
		fmt.Println("Loaded custom service target configuration")
	}

	warnDeprecatedScaleSettings(ServiceConfigProps(serviceConfig))

	// Provision any declared Foundry memory stores before deploying the agent, since
	// the agent's memory_search tool depends on the store existing at runtime.
	if err := p.provisionMemoryStores(
		ctx, serviceTargetConfig, azdEnv["FOUNDRY_PROJECT_ENDPOINT"], progress,
	); err != nil {
		return nil, err
	}

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
	var result *deployResult
	if agentDef.CodeConfiguration != nil {
		result, err = p.deployHostedCodeAgent(ctx, serviceConfig, serviceContext, progress, agentDef, azdEnv)
	} else {
		result, err = p.deployHostedAgent(ctx, serviceConfig, serviceContext, progress, agentDef, azdEnv)
	}
	if err != nil {
		return nil, err
	}

	// Poll until agent version is active
	if result.agentVersion.Status != "active" {
		agentClient := agent_api.NewAgentClient(
			azdEnv["FOUNDRY_PROJECT_ENDPOINT"],
			p.credential,
		)
		polledVersion, pollErr := p.waitForAgentActive(
			ctx, agentClient, result.agentName, result.agentVersion.Version, progress,
		)
		if pollErr != nil {
			return nil, pollErr
		}
		result.agentVersion = polledVersion
	} else {
		fmt.Fprintf(os.Stderr, "Agent version %s is already active.\n", result.agentVersion.Version)
	}

	// Patch agent-level endpoint/card fields
	if err := p.patchAgentEndpointFields(
		ctx, result.agentName, result.request.AgentEndpoint, result.request.AgentCard, azdEnv,
	); err != nil {
		return nil, err
	}

	return p.finalizeDeploy(ctx, progress, serviceConfig, azdEnv, result.agentVersion, result.protocols)
}

// provisionMemoryStores creates any Foundry memory stores declared in the service target
// configuration. Provisioning is idempotent: a store that already exists is left unchanged,
// so deployments are safe to re-run. Because azd never updates an existing store, a warning
// is emitted when a declared definition diverges from the live store so a changed azure.yaml
// value is not silently ignored. ChatModel and EmbeddingModel reference model deployment names
// that must already exist in the Foundry project.
func (p *AgentServiceTargetProvider) provisionMemoryStores(
	ctx context.Context,
	config *ServiceTargetAgentConfig,
	projectEndpoint string,
	progress azdext.ProgressReporter,
) error {
	if config == nil || len(config.MemoryStores) == 0 {
		return nil
	}

	// Validate every declared store up front, before talking to the service, so a bad
	// entry fails fast without half-provisioning the stores that precede it.
	if err := validateMemoryStores(config.MemoryStores); err != nil {
		return err
	}

	if projectEndpoint == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"cannot provision memory stores: the Foundry project endpoint is not set",
			"run 'azd provision' or connect to an existing project via "+
				"'azd ai agent init --project-id <resource-id>'",
		)
	}

	client := azure.NewFoundryMemoryStoreClient(projectEndpoint, p.credential)

	for _, store := range config.MemoryStores {
		if progress != nil {
			progress(fmt.Sprintf("Provisioning memory store %q", store.Name))
		}

		request := &azure.CreateMemoryStoreRequest{
			Name:        store.Name,
			Description: store.Description,
			Definition: azure.MemoryStoreDefinition{
				Kind:           azure.MemoryStoreKindDefault,
				ChatModel:      store.ChatModel,
				EmbeddingModel: store.EmbeddingModel,
				Options:        mapMemoryStoreOptions(store.Options),
			},
		}

		existing, created, err := client.EnsureMemoryStore(ctx, request)
		if err != nil {
			return exterrors.ServiceFromAzure(err, exterrors.OpProvisionMemoryStore)
		}

		if created {
			if progress != nil {
				progress(fmt.Sprintf("Created memory store %q", store.Name))
			}
			continue
		}

		// The store already exists. azd does not update existing stores, so surface any
		// azure.yaml changes that were not applied instead of silently ignoring them.
		if drift := memoryStoreDefinitionDrift(request.Definition, existing.Definition); len(drift) > 0 {
			writeMemoryStoreDriftWarning(store.Name, drift)
		} else if progress != nil {
			progress(fmt.Sprintf("Memory store %q already exists; leaving as-is", store.Name))
		}
	}

	return nil
}

// validateMemoryStores checks that every declared memory store has the required fields.
func validateMemoryStores(stores []MemoryStore) error {
	for _, store := range stores {
		if store.Name == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidMemoryStore,
				"a memory store in azure.yaml is missing the required 'name' field",
				"add a 'name' to each entry under the agent service 'memoryStores' list",
			)
		}
		if store.ChatModel == "" || store.EmbeddingModel == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidMemoryStore,
				fmt.Sprintf(
					"memory store '%s' must specify both 'chatModel' and 'embeddingModel'",
					store.Name,
				),
				"set 'chatModel' and 'embeddingModel' to model deployment names "+
					"available in your Foundry project",
			)
		}
	}

	return nil
}

// memoryStoreDefinitionDrift returns a human-readable list of the fields where the declared
// definition diverges from the live store. Only fields the user explicitly declared are
// compared, so unset options (which fall back to service defaults) never report false drift.
func memoryStoreDefinitionDrift(declared, live azure.MemoryStoreDefinition) []string {
	var drift []string

	if declared.ChatModel != live.ChatModel {
		drift = append(drift, fmt.Sprintf("chatModel (declared %q, current %q)",
			declared.ChatModel, live.ChatModel))
	}
	if declared.EmbeddingModel != live.EmbeddingModel {
		drift = append(drift, fmt.Sprintf("embeddingModel (declared %q, current %q)",
			declared.EmbeddingModel, live.EmbeddingModel))
	}

	if declared.Options == nil {
		return drift
	}

	var liveOpts azure.MemoryStoreOptions
	if live.Options != nil {
		liveOpts = *live.Options
	}

	if boolPtrDiffers(declared.Options.ChatSummaryEnabled, liveOpts.ChatSummaryEnabled) {
		drift = append(drift, fmt.Sprintf("options.chatSummaryEnabled (declared %v)",
			*declared.Options.ChatSummaryEnabled))
	}
	if boolPtrDiffers(declared.Options.UserProfileEnabled, liveOpts.UserProfileEnabled) {
		drift = append(drift, fmt.Sprintf("options.userProfileEnabled (declared %v)",
			*declared.Options.UserProfileEnabled))
	}
	if boolPtrDiffers(declared.Options.ProceduralMemoryEnabled, liveOpts.ProceduralMemoryEnabled) {
		drift = append(drift, fmt.Sprintf("options.proceduralMemoryEnabled (declared %v)",
			*declared.Options.ProceduralMemoryEnabled))
	}
	if declared.Options.DefaultTTLSeconds != nil &&
		(liveOpts.DefaultTTLSeconds == nil || *declared.Options.DefaultTTLSeconds != *liveOpts.DefaultTTLSeconds) {
		drift = append(drift, fmt.Sprintf("options.defaultTtlSeconds (declared %d)",
			*declared.Options.DefaultTTLSeconds))
	}
	if declared.Options.UserProfileDetails != "" &&
		declared.Options.UserProfileDetails != liveOpts.UserProfileDetails {
		drift = append(drift, "options.userProfileDetails")
	}

	return drift
}

// boolPtrDiffers reports whether a declared bool pointer is set and differs from the live value.
func boolPtrDiffers(declared, live *bool) bool {
	if declared == nil {
		return false
	}
	return live == nil || *declared != *live
}

// writeMemoryStoreDriftWarning warns that azure.yaml changes were not applied to an existing store.
func writeMemoryStoreDriftWarning(name string, drift []string) {
	fmt.Fprintf(os.Stderr, "%s", output.WithWarningFormat(
		"Memory store %q already exists; azd does not update existing memory stores, so the "+
			"following azure.yaml change(s) were NOT applied: %s. To apply them, delete the store "+
			"in the Foundry portal (or give it a new name) and redeploy.\n",
		name, strings.Join(drift, ", "),
	))
}

// mapMemoryStoreOptions converts the azure.yaml memory store options into the API request shape.
// It returns nil when no options are configured (or all fields are unset) so the service applies
// its own defaults, rather than sending an empty options object that the service might treat
// differently from an omitted one.
func mapMemoryStoreOptions(options *MemoryStoreOptions) *azure.MemoryStoreOptions {
	if options == nil || memoryStoreOptionsEmpty(options) {
		return nil
	}

	return &azure.MemoryStoreOptions{
		ChatSummaryEnabled:      options.ChatSummaryEnabled,
		UserProfileEnabled:      options.UserProfileEnabled,
		ProceduralMemoryEnabled: options.ProceduralMemoryEnabled,
		DefaultTTLSeconds:       options.DefaultTtlSeconds,
		UserProfileDetails:      options.UserProfileDetails,
	}
}

// memoryStoreOptionsEmpty reports whether every memory store option field is unset.
func memoryStoreOptionsEmpty(options *MemoryStoreOptions) bool {
	return options.ChatSummaryEnabled == nil &&
		options.UserProfileEnabled == nil &&
		options.ProceduralMemoryEnabled == nil &&
		options.DefaultTtlSeconds == nil &&
		options.UserProfileDetails == ""
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

	if p.shouldSkipACRForEnvironment(ctx) {
		log.Printf("AZD_AGENT_SKIP_ACR=true: using pre-built image from agent.yaml")
		return true, nil
	}

	// Default to build so the pre-built path requires an explicit choice.
	// In non-interactive mode (--no-prompt), the framework returns the default
	// selection (index 0 = build) automatically unless AZD_AGENT_SKIP_ACR=true
	// was set by init --image.
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

func (p *AgentServiceTargetProvider) shouldSkipACRForEnvironment(ctx context.Context) bool {
	if p.env == nil || p.env.Name == "" {
		return false
	}

	resp, err := p.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: p.env.Name,
		Key:     "AZD_AGENT_SKIP_ACR",
	})
	if err != nil || resp == nil {
		return false
	}

	return strings.EqualFold(strings.TrimSpace(resp.Value), "true")
}

// isCodeDeployAgent returns true if the agent definition has code_configuration (code deploy mode)
func (p *AgentServiceTargetProvider) isCodeDeployAgent() bool {
	agentDef, isHosted, err := p.loadContainerAgentDefinition()
	if err != nil || !isHosted {
		return false
	}

	return agentDef.CodeConfiguration != nil
}

// deployPrepResult holds the common outputs from prepareDeploy, used by both
// container and code deploy paths.
type deployPrepResult struct {
	resolvedEnvVars map[string]string
	request         *agent_api.CreateAgentRequest
	protocols       []agent_yaml.ProtocolVersionRecord
}

func writeExistingAgentVersionWarning(agentName string) {
	fmt.Fprintf(os.Stderr, "%s", agents.ExistingAgentWarning(agentName))
}

func writeExistingAgentVersionWarningIfPresent(
	ctx context.Context,
	agentChecker agents.AgentChecker,
	agentName string,
) bool {
	exists, err := agents.AgentExists(ctx, agentChecker, agentName, agent_api.AgentEndpointAPIVersion)
	if err != nil {
		log.Printf("existing agent name check skipped for %q: %v", agentName, err)
		return false
	}
	if exists {
		writeExistingAgentVersionWarning(agentName)
		return true
	}

	return false
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
	if azdEnv["FOUNDRY_PROJECT_ENDPOINT"] == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"FOUNDRY_PROJECT_ENDPOINT is required: environment variable was not found in the current azd environment",
			"run 'azd provision' or connect to an existing project via 'azd ai agent init --project-id <resource-id>'",
		)
	}

	if p.agentDefinitionPath != "" {
		fmt.Fprintf(os.Stderr, "Loaded configuration from: %s\n", p.agentDefinitionPath)
	}
	fmt.Fprintf(os.Stderr, "Using endpoint: %s\n", azdEnv["FOUNDRY_PROJECT_ENDPOINT"])
	fmt.Fprintf(os.Stderr, "Agent Name: %s\n", agentDef.Name)

	// Resolve environment variables from YAML using azd environment values
	resolvedEnvVars := make(map[string]string)
	if agentDef.EnvironmentVariables != nil {
		for _, envVar := range *agentDef.EnvironmentVariables {
			resolvedEnvVars[envVar.Name] = p.resolveEnvironmentVariables(envVar.Value, azdEnv)
		}
	}

	// Parse service config for container resource overrides
	foundryAgentConfig, err := LoadServiceTargetAgentConfig(serviceConfig)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to parse foundry agent config: %s", err),
			"check the service configuration in azure.yaml",
		)
	}

	warnDeprecatedScaleSettings(ServiceConfigProps(serviceConfig))

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

// deployResult holds the intermediate results from a deploy method (code or container)
// before the common post-deploy steps (polling, patching, finalization) are applied.
type deployResult struct {
	agentVersion *agent_api.AgentVersionObject
	agentName    string
	protocols    []agent_yaml.ProtocolVersionRecord
	request      *agent_api.CreateAgentRequest
}

// patchAgentEndpointFields patches agent-level fields (agent_endpoint, agent_card).
// These are agent-level properties, not version-level, so they require a separate PatchAgent call.
func (p *AgentServiceTargetProvider) patchAgentEndpointFields(
	ctx context.Context,
	agentName string,
	agentEndpoint *agent_api.AgentEndpoint,
	agentCard *agent_api.AgentCard,
	azdEnv map[string]string,
) error {
	if agentEndpoint == nil && agentCard == nil {
		return nil
	}

	agentClient := agent_api.NewAgentClient(
		azdEnv["FOUNDRY_PROJECT_ENDPOINT"],
		p.credential,
	)

	patchRequest := &agent_api.PatchAgentRequest{
		AgentEndpoint: agentEndpoint,
		AgentCard:     agentCard,
	}

	_, err := agentClient.PatchAgent(ctx, agentName, patchRequest, agent_api.AgentEndpointAPIVersion)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
	}

	fmt.Fprintf(os.Stderr, "Agent endpoint/card updated.\n")
	return nil
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
		azdEnv["FOUNDRY_PROJECT_ENDPOINT"],
		protocols,
	)

	// Best-effort: enrich the last endpoint artifact's note with a
	// context-aware "Next:" block. Failures are non-fatal — the static
	// aka.ms link emitted by deployArtifacts is preserved when the
	// enrichment is skipped or short-circuits.
	if state, _ := nextstep.AssembleState(ctx, p.azdClient); state != nil {
		// Scope to the service just deployed. ResolveAfterDeploy renders a
		// show/invoke pair per state.Services entry; without this filter a
		// multi-agent project would attach guidance for other services to
		// this artifact's note.
		state.Services = filterServicesByName(state.Services, serviceConfig.Name)

		projectRoot := ""
		if proj, err := p.azdClient.Project().Get(ctx, nil); err == nil && proj.Project != nil {
			projectRoot = proj.Project.Path
		}
		configDir := ""
		if projectRoot != "" && p.env != nil && p.env.Name != "" {
			configDir = filepath.Join(projectRoot, ".azure", p.env.Name)
		}
		augmentDeployNote(state, artifacts, projectRoot, configDir)
	}

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
) (*deployResult, error) {
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

	return &deployResult{
		agentVersion: agentVersionResponse,
		agentName:    prep.request.Name,
		protocols:    prep.protocols,
		request:      prep.request,
	}, nil
}

// packageCodeDeploy creates a ZIP archive of the agent source code, writes it to a temp file,
// and computes its SHA-256. Returns the temp file path and SHA-256 hex string.
func (p *AgentServiceTargetProvider) packageCodeDeploy(ctx context.Context, serviceConfig *azdext.ServiceConfig) (string, string, error) {
	// Source directory is the service's directory. When AGENT_DEFINITION_PATH
	// overrides the definition, its file may live outside the service path, so
	// zip the override's directory to capture the right source tree. Fall back to
	// the definition directory when the service path was not resolved.
	srcDir := p.servicePath
	if os.Getenv("AGENT_DEFINITION_PATH") != "" && p.agentDefinitionPath != "" {
		srcDir = filepath.Dir(p.agentDefinitionPath)
	} else if srcDir == "" {
		srcDir = filepath.Dir(p.agentDefinitionPath)
	}

	// Check runtime and dependency resolution for dotnet bundled mode
	if agentDef, isHosted, err := p.loadContainerAgentDefinition(); err == nil && isHosted &&
		agentDef.CodeConfiguration != nil {
		isDotnet := strings.HasPrefix(agentDef.CodeConfiguration.Runtime, "dotnet_")
		isBundled := false // default is remote_build (matches promptCodeConfig and deployHostedCodeAgent defaults)
		if agentDef.CodeConfiguration.DependencyResolution != nil {
			isBundled = *agentDef.CodeConfiguration.DependencyResolution == "bundled"
		}
		if isDotnet && isBundled {
			return p.packageDotnetBundled(srcDir)
		}

		// Python bundled: validate that dependencies are installed in srcDir
		isPython := strings.HasPrefix(agentDef.CodeConfiguration.Runtime, "python_")
		if isPython && isBundled {
			if err := validatePythonBundledDeps(srcDir); err != nil {
				return "", "", err
			}
		}
	}

	return zipSourceDir(ctx, srcDir)
}

// zipSourceDir creates a ZIP archive of srcDir honoring .agentignore, writes it to a
// temp file, and computes its SHA-256. It returns the temp file path and SHA-256 hex
// string.
func zipSourceDir(ctx context.Context, srcDir string) (string, string, error) {
	// Load .agentignore (or use defaults if no file exists)
	ignoreMatcher, err := newAgentIgnoreMatcher(ctx, srcDir)
	if err != nil {
		return "", "", exterrors.Dependency(
			exterrors.CodeInvalidFilePath,
			fmt.Sprintf("failed to load %s: %s", agentIgnoreFileName, err),
			"check that .agentignore is a valid file with gitignore syntax",
		)
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

		// Skip symlinked directories to avoid traversing outside the project root
		if d.IsDir() && d.Type()&fs.ModeSymlink != 0 {
			return filepath.SkipDir
		}

		// Check directory exclusions
		if d.IsDir() {
			if ignoreMatcher.ShouldExclude(relPath, true) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip symlinks to avoid including files outside the agent directory
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		// Check file exclusions
		if ignoreMatcher.ShouldExclude(relPath, false) {
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

	// Enforce maximum ZIP size (250 MB)
	const maxZipSize = 250 * 1024 * 1024
	fi, err := os.Stat(tmpPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to stat ZIP file: %w", err)
	}
	if fi.Size() > maxZipSize {
		return "", "", fmt.Errorf(
			"code package too large: %d MB (max 250 MB). Reduce package size by excluding unnecessary files or using remote_build for dependency resolution",
			fi.Size()/(1024*1024),
		)
	}

	sha256Hex := hex.EncodeToString(hasher.Sum(nil))
	success = true

	return tmpPath, sha256Hex, nil
}

// packageDotnetBundled runs "dotnet publish" for the .NET project and creates a ZIP of the published output.
func (p *AgentServiceTargetProvider) packageDotnetBundled(srcDir string) (string, string, error) {
	// Find the .csproj file
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to read source directory: %w", err)
	}

	var csprojPath string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".csproj") {
			csprojPath = filepath.Join(srcDir, e.Name())
			break
		}
	}
	if csprojPath == "" {
		return "", "", fmt.Errorf("no .csproj file found in %s; required for dotnet bundled packaging", srcDir)
	}

	// Create temp directory for publish output
	publishDir, err := os.MkdirTemp("", "azd-dotnet-publish-*")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp dir for dotnet publish: %w", err)
	}
	defer os.RemoveAll(publishDir)

	// Run dotnet publish targeting linux (hosted agents run on linux)
	fmt.Fprintf(os.Stderr, "Running 'dotnet publish' for bundled packaging...\n")
	cmd := exec.Command("dotnet", "publish", csprojPath, //nolint:gosec // csprojPath is derived from user's project directory
		"-c", "Release",
		"-r", "linux-x64",
		"--self-contained", "false",
		"-o", publishDir,
	)
	cmd.Dir = srcDir
	var publishOutput bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stderr, &publishOutput)
	cmd.Stderr = io.MultiWriter(os.Stderr, &publishOutput)
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("dotnet publish failed: %w\nOutput:\n%s", err, publishOutput.String())
	}

	// ZIP the publish output
	tmpFile, err := os.CreateTemp("", "azd-code-deploy-*.zip")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp file for ZIP: %w", err)
	}
	tmpPath := tmpFile.Name()

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

	err = filepath.WalkDir(publishDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, relErr := filepath.Rel(publishDir, path)
		if relErr != nil {
			return relErr
		}

		if relPath == "." {
			return nil
		}

		relPath = filepath.ToSlash(relPath)

		if d.IsDir() {
			return nil
		}

		fileData, readErr := os.ReadFile(path) //nolint:gosec // path from WalkDir within temp publish dir
		if readErr != nil {
			return fmt.Errorf("failed to read %s: %w", relPath, readErr)
		}

		w, createErr := zipWriter.Create(relPath)
		if createErr != nil {
			return fmt.Errorf("failed to create ZIP entry %s: %w", relPath, createErr)
		}

		if _, writeErr := w.Write(fileData); writeErr != nil {
			return fmt.Errorf("failed to write ZIP entry %s: %w", relPath, writeErr)
		}

		return nil
	})

	if err != nil {
		return "", "", fmt.Errorf("failed to walk publish directory: %w", err)
	}

	if err := zipWriter.Close(); err != nil {
		return "", "", fmt.Errorf("failed to close ZIP: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return "", "", fmt.Errorf("failed to close temp file: %w", err)
	}

	// Enforce maximum ZIP size (250 MB) — same limit as packageCodeDeploy
	const maxZipSizeBundled = 250 * 1024 * 1024
	if fi, statErr := os.Stat(tmpPath); statErr == nil && fi.Size() > maxZipSizeBundled {
		return "", "", fmt.Errorf(
			"bundled package too large: %d MB (max 250 MB). Consider using remote_build for dependency resolution",
			fi.Size()/(1024*1024),
		)
	}

	sha256Hex := hex.EncodeToString(hasher.Sum(nil))
	success = true

	return tmpPath, sha256Hex, nil
}

// validatePythonBundledDeps checks that a Python project in bundled mode has
// installed dependencies in the source directory. It looks for .dist-info
// directories which are always created by pip install --target.
// Only returns an error if requirements.txt exists AND has content AND no
// .dist-info directories are found — this avoids false positives.
func validatePythonBundledDeps(srcDir string) error {
	// Check if requirements.txt exists and has non-empty content
	reqPath := filepath.Join(srcDir, "requirements.txt")
	data, err := os.ReadFile(reqPath) //nolint:gosec // path from internal state
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No requirements.txt — nothing to validate
			return nil
		}
		return exterrors.Dependency(
			exterrors.CodeInvalidFilePath,
			fmt.Sprintf("failed to read requirements.txt: %s", err),
			"check file permissions for "+reqPath,
		)
	}

	// Check if requirements.txt has any non-comment, non-empty lines
	hasRequirements := false
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			hasRequirements = true
			break
		}
	}
	if !hasRequirements {
		return nil
	}

	// Look for any *.dist-info directory in srcDir (top-level only, which is
	// where pip install --target . places them). Also check one level deep
	// for common patterns like vendor/ or lib/.
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeInvalidFilePath,
			fmt.Sprintf("failed to read source directory: %s", err),
			"check that the source directory exists and is readable: "+srcDir,
		)
	}

	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".dist-info") {
			// Found at least one installed package — pass
			return nil
		}
	}

	// Check one level of subdirectories for .dist-info (e.g., vendor/, lib/)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subEntries, err := os.ReadDir(filepath.Join(srcDir, e.Name()))
		if err != nil {
			continue
		}
		for _, se := range subEntries {
			if se.IsDir() && strings.HasSuffix(se.Name(), ".dist-info") {
				return nil
			}
		}
	}

	return exterrors.Dependency(
		exterrors.CodeBundledDepsNotFound,
		"bundled mode is configured but no installed packages were found in the source directory. "+
			"Dependencies must be installed locally before deploying",
		"run: pip install -r requirements.txt -t \""+srcDir+"\""+
			" --platform manylinux_2_17_x86_64 --platform linux_x86_64 --platform any"+
			" --implementation cp --only-binary=:all:",
	)
}

// deployHostedCodeAgent deploys a code-based hosted agent via multipart ZIP upload.
func (p *AgentServiceTargetProvider) deployHostedCodeAgent(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
	agentDef agent_yaml.ContainerAgent,
	azdEnv map[string]string,
) (*deployResult, error) {
	progress("Deploying hosted agent (code deploy)")

	// Validate that AZURE_LOCATION is set (region validation is handled server-side;
	// code deploy is supported in all hosted-agent regions).
	if strings.TrimSpace(azdEnv["AZURE_LOCATION"]) == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeAgentCreateFailed,
			"AZURE_LOCATION is not set; the Foundry project region is required for code deploy",
			"run 'azd provision' or 'azd ai agent init' to set the project location",
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
		cmdPrefix := agent_yaml.RuntimeCmdPrefix(agentDef.CodeConfiguration.Runtime)
		fmt.Fprintf(os.Stderr, "Entry Point: [\"%s\", \"%s\"]\n", cmdPrefix, agentDef.CodeConfiguration.EntryPoint)
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
		azdEnv["FOUNDRY_PROJECT_ENDPOINT"],
		p.credential,
	)

	// Check if agent already exists (GET /agents/{name})
	progress("Creating agent")
	_, getErr := agentClient.GetAgent(ctx, agentDef.Name, agent_api.AgentEndpointAPIVersion)
	var agentResp *agent_api.AgentObject

	if getErr != nil {
		// Only fall back to create on 404; propagate other errors (auth, 5xx, network)
		if respErr, ok := errors.AsType[*azcore.ResponseError](getErr); !ok || respErr.StatusCode != http.StatusNotFound {
			return nil, fmt.Errorf("failed to check if agent exists: %w", getErr)
		}
		// Agent doesn't exist — create
		fmt.Fprintf(os.Stderr, "Creating new agent: %s\n", agentDef.Name)
		agentResp, err = agentClient.CreateAgentFromZip(
			ctx, agentDef.Name, versionRequest, zipData, sha256Hex, agent_api.AgentEndpointAPIVersion,
		)
		if err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeAgentCreateFailed,
				fmt.Sprintf("failed to create agent from ZIP: %s; check the agent definition and try again", err),
			)
		}
	} else {
		// Agent exists — update
		writeExistingAgentVersionWarning(agentDef.Name)
		agentResp, err = agentClient.UpdateAgentFromZip(
			ctx, agentDef.Name, versionRequest, zipData, sha256Hex, agent_api.AgentEndpointAPIVersion,
		)
		if err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeAgentCreateFailed,
				fmt.Sprintf("failed to update agent from ZIP: %s; check the agent definition and try again", err),
			)
		}
	}

	return &deployResult{
		agentVersion: &agentResp.Versions.Latest,
		agentName:    agentDef.Name,
		protocols:    prep.protocols,
		request:      prep.request,
	}, nil
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
				"https://aka.ms/azd-agents-invoke") +
				"\n\nSet up an evaluation suite to measure quality and impact in one step with " + output.WithHighLightFormat("azd ai agent eval generate")
		}
	}

	return artifacts
}

// augmentDeployNote enriches the last endpoint artifact's note with a
// context-aware "Next:" block resolved from the provided state.
//
// Collision rule with the static aka.ms link emitted by deployArtifacts:
//
//   - When the resolved block contains a "see <relPath>/README.md"
//     suggestion (i.e. a local README exists at the service path), the
//     aka.ms line is replaced entirely — the block already points the
//     user at the more-detailed local doc, so the canned link is
//     redundant.
//   - Otherwise the aka.ms line is preserved and the "Next:" block is
//     appended below, separated by a single blank line — aka.ms remains
//     the fallback doc pointer when no local README is present. The block
//     returned by FormatNextForNote already starts with a newline, so the
//     append joins with a single "\n" to avoid a double blank line.
//
// The function is a no-op when state is nil, no artifact carries a note,
// or the resolver returns no suggestions; this keeps the deploy path
// resilient to partial state (e.g. project metadata unavailable) without
// silencing the original static guidance.
func augmentDeployNote(state *nextstep.State, artifacts []*azdext.Artifact, projectRoot, configDir string) {
	if state == nil {
		return
	}

	target := lastNoteArtifact(artifacts)
	if target == nil {
		return
	}

	cachedPayload := func(serviceName string) string {
		if configDir == "" || serviceName == "" {
			return ""
		}
		spec, err := nextstep.ReadCachedOpenAPISpec(configDir, serviceName, "local")
		if err != nil {
			return ""
		}
		return nextstep.ExtractInvokeExample(spec)
	}

	readmeExists := func(relativePath string) bool {
		if projectRoot == "" {
			return false
		}
		// Only consider the canonical casing — ResolveAfterDeploy emits
		// "see <relPath>/README.md" verbatim. Accepting other casings here
		// would yield a broken pointer on case-sensitive filesystems and,
		// because suggestionsIncludeReadme triggers the replace branch,
		// would silently discard the working aka.ms fallback.
		readmePath, err := paths.JoinAllowRoot(projectRoot, relativePath, "README.md")
		if err != nil {
			return false
		}
		_, err = os.Stat(readmePath)
		return err == nil
	}

	suggestions := nextstep.ResolveAfterDeploy(state, cachedPayload, readmeExists)
	if len(suggestions) == 0 {
		return
	}

	block := nextstep.FormatNextForNote(suggestions)
	if block == "" {
		return
	}

	if suggestionsIncludeReadme(suggestions) {
		target.Metadata["note"] = block
		return
	}
	existing := target.Metadata["note"]
	if existing == "" {
		target.Metadata["note"] = block
		return
	}
	// FormatNextForNote prefixes block with its own leading newline, so a
	// single "\n" here yields exactly one blank line between the preserved
	// aka.ms note and the "Next:" header.
	target.Metadata["note"] = existing + "\n" + block
}

// lastNoteArtifact returns the last artifact in the slice whose
// Metadata["note"] is non-empty, or nil when none of the artifacts
// carry a note. deployArtifacts attaches its informational note to the
// final endpoint artifact only; scanning from the end keeps this in
// sync should the convention shift to multi-note artifacts in future.
func lastNoteArtifact(artifacts []*azdext.Artifact) *azdext.Artifact {
	for i := len(artifacts) - 1; i >= 0; i-- {
		a := artifacts[i]
		if a == nil || a.Metadata == nil {
			continue
		}
		if a.Metadata["note"] != "" {
			return a
		}
	}
	return nil
}

// suggestionsIncludeReadme reports whether any suggestion is a local-README
// pointer (ResolveAfterDeploy emits these as "see <relPath>/README.md").
// Used by augmentDeployNote to decide whether to replace or append to the
// existing static aka.ms note.
func suggestionsIncludeReadme(suggestions []nextstep.Suggestion) bool {
	for _, s := range suggestions {
		if strings.HasPrefix(s.Command, "see ") && strings.HasSuffix(s.Command, "README.md") {
			return true
		}
	}
	return false
}

// filterServicesByName narrows the assembled state's service slice to a
// single entry by name. Used by the deploy hook so the rendered "Next:"
// block reflects only the service whose artifact note is being augmented,
// not every agent service in the project.
func filterServicesByName(services []nextstep.ServiceState, name string) []nextstep.ServiceState {
	if name == "" {
		return services
	}
	for i := range services {
		if services[i].Name == name {
			return services[i : i+1]
		}
	}
	return nil
}

// protocolEndpointInfo holds a displayable protocol label and its invocation URL.
type protocolEndpointInfo struct {
	Protocol string
	URL      string
}

// displayableProtocolFor returns the displayable protocol entry matching the given
// protocol string, or nil if the protocol does not produce a user-visible endpoint.
func displayableProtocolFor(protocol string) *displayableProtocolEntry {
	for i, dp := range displayableProtocols {
		if agent_api.AgentProtocol(protocol) == dp.Protocol {
			return &displayableProtocols[i]
		}
	}
	return nil
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
		dp := displayableProtocolFor(p.Protocol)
		if dp == nil {
			continue
		}

		var endpointURL string
		if dp.BuildURL != nil {
			endpointURL = dp.BuildURL(projectEndpoint, agentName)
			if endpointURL == "" {
				// A protocol builder may decline to produce a URL when its inputs
				// cannot yield a callable endpoint (e.g. a malformed projectEndpoint
				// that fails to parse, for invocations_ws). Skip rather than
				// registering a broken URL.
				continue
			}
		} else {
			endpointURL = fmt.Sprintf(
				"%s/agents/%s/endpoint/protocols/%s", projectEndpoint, agentName, dp.URLPath,
			)
			if !strings.HasPrefix(dp.URLPath, "openai/") {
				endpointURL += fmt.Sprintf("?api-version=%s", agent_api.AgentEndpointAPIVersion)
			}
		}

		endpoints = append(endpoints, protocolEndpointInfo{
			Protocol: p.Protocol,
			URL:      endpointURL,
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

// waitForAgentActive polls the agent version until it reaches a confirmed terminal state.
// It requires 2 consecutive polls with the same terminal status ("active" or "failed") to confirm,
// avoiding transient service-side flickers. Returns the final AgentVersionObject or an error.
func (p *AgentServiceTargetProvider) waitForAgentActive(
	ctx context.Context,
	agentClient *agent_api.AgentClient,
	agentName string,
	version string,
	progress azdext.ProgressReporter,
) (*agent_api.AgentVersionObject, error) {
	const pollInterval = 10 * time.Second
	const pollTimeout = 5 * time.Minute
	const confirmCount = 2 // consecutive times a terminal status must be seen

	deadline := time.Now().Add(pollTimeout)
	maxAttempts := int(pollTimeout / pollInterval)
	attempt := 0
	progress("Waiting for agent to become active")

	var consecutiveActive int
	var consecutiveFailed int
	var lastVersion *agent_api.AgentVersionObject

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("deployment cancelled: %w", ctx.Err())
		case <-time.After(pollInterval):
		}

		attempt++
		progress(fmt.Sprintf("Polling agent status (%d/%d)", attempt, maxAttempts))

		versionResp, err := agentClient.GetAgentVersion(ctx, agentName, version, agent_api.AgentEndpointAPIVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: poll failed: %s\n", err)
			// Reset counters on error — don't count transient failures
			consecutiveActive = 0
			consecutiveFailed = 0
			continue
		}
		lastVersion = versionResp

		switch versionResp.Status {
		case "active":
			consecutiveActive++
			consecutiveFailed = 0
			if consecutiveActive >= confirmCount {
				fmt.Fprintf(os.Stderr, "Agent version %s is active!\n", version)
				return versionResp, nil
			}
			fmt.Fprintf(os.Stderr, "  Status: active (confirming...)\n")
		case "failed":
			consecutiveFailed++
			consecutiveActive = 0
			if consecutiveFailed >= confirmCount {
				errMsg := "agent deployment failed"
				if versionResp.Error != nil {
					errMsg = fmt.Sprintf("agent deployment failed: [%s] %s", versionResp.Error.Code, versionResp.Error.Message)
				}
				if versionResp.RequestID != "" {
					errMsg += fmt.Sprintf(" (request-id: %s)", versionResp.RequestID)
				}
				return nil, exterrors.Internal(exterrors.CodeAgentCreateFailed, errMsg)
			}
			fmt.Fprintf(os.Stderr, "  Status: failed (confirming...)\n")
		default:
			consecutiveActive = 0
			consecutiveFailed = 0
			fmt.Fprintf(os.Stderr, "  Status: %s...\n", versionResp.Status)
		}
	}

	// Timeout
	lastStatus := "unknown"
	if lastVersion != nil {
		lastStatus = lastVersion.Status
	}
	return nil, exterrors.Internal(
		exterrors.CodeAgentCreateFailed,
		fmt.Sprintf("agent deployment timed out (last status: %s); check agent status manually", lastStatus),
	)
}

// createAgent creates a new version of the agent using the API
func (p *AgentServiceTargetProvider) createAgent(
	ctx context.Context,
	request *agent_api.CreateAgentRequest,
	azdEnv map[string]string,
) (*agent_api.AgentVersionObject, error) {
	// Create agent client
	agentClient := agent_api.NewAgentClient(
		azdEnv["FOUNDRY_PROJECT_ENDPOINT"],
		p.credential,
	)

	writeExistingAgentVersionWarningIfPresent(ctx, agentClient, request.Name)

	// Extract CreateAgentVersionRequest from CreateAgentRequest
	versionRequest := &agent_api.CreateAgentVersionRequest{
		Description: request.Description,
		Metadata:    request.Metadata,
		Definition:  request.Definition,
	}

	// Create agent version
	agentVersionResponse, err := agentClient.CreateAgentVersion(
		ctx, request.Name, versionRequest, agent_api.AgentEndpointAPIVersion,
	)
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
	}

	fmt.Fprintf(os.Stderr, "Agent version '%s' created successfully!\n", agentVersionResponse.Name)

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
		if hostedDef.ContainerConfiguration != nil && hostedDef.ContainerConfiguration.Image != "" {
			fmt.Fprintf(os.Stderr, "Image: %s\n", hostedDef.ContainerConfiguration.Image)
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
	projectEndpoint := strings.TrimRight(azdEnv["FOUNDRY_PROJECT_ENDPOINT"], "/")
	envVars[baseEndpointKey] = fmt.Sprintf(
		"%s/agents/%s/versions/%s", projectEndpoint, agentVersionResponse.Name, agentVersionResponse.Version,
	)

	endpoints := agentInvocationEndpoints(
		azdEnv["FOUNDRY_PROJECT_ENDPOINT"],
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
	resolved, err := ExpandEnv(value, func(varName string) string {
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
	if err := p.ensureEnv(ctx); err != nil {
		return err
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
