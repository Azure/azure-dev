// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

// cSpell:ignore containerref

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
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/agentkind"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/pkg/botservice"
	"azureaiagent/internal/pkg/containerref"
	"azureaiagent/internal/pkg/envkey"
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

func endpointHost(endpoint string) string {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// buildInvocationsProtocolURL builds the per-agent HTTPS URL for the "invocations" protocol.
func buildInvocationsProtocolURL(projectEndpoint, agentName string) string {
	return fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/invocations?api-version=%s",
		projectEndpoint, agentName, agent_api.AgentEndpointAPIVersion,
	)
}

// buildInvocationsWSProtocolURL builds the Foundry data-plane WebSocket URL for the
// "invocations_ws" protocol. The route is path-based and mirrors the HTTP "invocations" shape:
// the project and agent are carried as path segments under
// /api/projects/<project>/agents/<agent>/endpoint/protocols/invocations_ws, differing only in the
// wss:// scheme and the trailing invocations_ws segment. The only WebSocket query parameters are
// the required api-version and an optional agent_session_id; callers must add their own
// agent_session_id when establishing a session.
//
// Returns "" if projectEndpoint cannot be parsed into a URL with a host: the route requires a host
// (for the wss:// authority) to be callable, so emitting a partial URL would only register a
// non-callable endpoint. Callers (agentInvocationEndpoints) filter out empty results.
func buildInvocationsWSProtocolURL(projectEndpoint, agentName string) string {
	projectEndpoint = strings.TrimSpace(projectEndpoint)
	u, err := url.Parse(projectEndpoint)
	if err != nil || u.Host == "" {
		return ""
	}

	return fmt.Sprintf(
		"wss://%s%s/agents/%s/endpoint/protocols/invocations_ws?api-version=%s",
		u.Host, strings.TrimRight(u.Path, "/"), agentName, agent_api.AgentEndpointAPIVersion,
	)
}

func buildVoiceWSProtocolURL(projectEndpoint, agentName string) string {
	projectEndpoint = strings.TrimSpace(projectEndpoint)
	u, err := url.Parse(projectEndpoint)
	if err != nil || u.Host == "" {
		return ""
	}

	return fmt.Sprintf(
		"wss://%s%s/agents/%s/endpoint/protocols/voice?api-version=%s",
		u.Host, strings.TrimRight(u.Path, "/"), agentName, agent_api.AgentEndpointAPIVersion,
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
	// serviceConfigResolved tracks whether serviceConfig has had
	// its local $ref includes expanded. Cleared whenever a newer
	// config is adopted.
	serviceConfigResolved bool
	credential            *azidentity.AzureDeveloperCLICredential
	tenantId              string
	env                   *azdext.Environment
	foundryProject        *arm.ResourceID
	projectServices       map[string]*azdext.ServiceConfig
	dependencyEnabled     dependencyEnabled
	dependencyEnv         map[string]string
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
	p.adoptServiceConfig(serviceConfig)
	return nil
}

// adoptServiceConfig stores the service config azd core supplied
// for the current call. Core re-expands ${VAR} references against
// the environment on every request, so a deploy-time config can
// carry values the Initialize-time snapshot lacked, for example a
// location the user was prompted for during provision. Keeping the
// newest config avoids deploying with the empty strings that unset
// variables expand to.
func (p *AgentServiceTargetProvider) adoptServiceConfig(serviceConfig *azdext.ServiceConfig) {
	if serviceConfig == nil || serviceConfig == p.serviceConfig {
		return
	}
	p.serviceConfig = serviceConfig
	p.serviceConfigResolved = false
}

// resolveServiceConfig expands local $ref includes on the current
// service config. It is idempotent per config instance, so repeat
// calls stay cheap while a freshly adopted config is always
// re-resolved.
func (p *AgentServiceTargetProvider) resolveServiceConfig() error {
	if p.serviceConfigResolved || p.serviceConfig == nil || p.projectPath == "" {
		return nil
	}
	if err := ResolveServiceConfigInPlace(p.serviceConfig, p.projectPath); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf(
				"failed to resolve service config for %s: %s",
				p.serviceConfig.Name,
				err,
			),
			"fix the agent service configuration in azure.yaml",
		)
	}
	p.serviceConfigResolved = true
	return nil
}

// ensureDeployContext lazily resolves the agent definition file, the azd
// environment, the tenant, and the credential. Idempotent via the
// agentDefinitionPath short-circuit. The short-circuit still resolves
// the service config so a newer one adopted after the first
// deploy-time call is expanded before consumers read it.
func (p *AgentServiceTargetProvider) ensureDeployContext(ctx context.Context) error {
	if p.deployContextReady || p.agentDefinitionPath != "" {
		return p.resolveServiceConfig()
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
	p.projectPath = proj.Project.Path
	p.projectServices = proj.GetProject().GetServices()
	p.dependencyEnabled = p.isDependencyEnabled
	if err := p.resolveServiceConfig(); err != nil {
		return err
	}
	servicePath := p.serviceConfig.GetRelativePath()
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
	if _, _, found, _, defErr := AgentDefinitionFromResolvedService(
		p.serviceConfig,
		proj.Project.Path,
	); defErr != nil {
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

func (p *AgentServiceTargetProvider) isDependencyEnabled(ctx context.Context, serviceName string) (bool, error) {
	resp, err := p.azdClient.Project().GetServiceConfigValue(ctx, &azdext.GetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "condition",
	})
	if err != nil {
		return false, fmt.Errorf("read deployment condition for service %q: %w", serviceName, err)
	}
	if !resp.GetFound() {
		return true, nil
	}
	conditionValue, err := dependencyConditionValue(resp.GetValue())
	if err != nil {
		return false, fmt.Errorf("read deployment condition for service %q: %w", serviceName, err)
	}
	if strings.TrimSpace(conditionValue) == "" {
		return true, nil
	}
	condition, err := ExpandEnv(conditionValue, func(name string) string {
		return p.dependencyEnvValue(name)
	})
	if err != nil {
		return false, fmt.Errorf("malformed deployment condition for service %q: %w", serviceName, err)
	}
	// Keep this list aligned with pkg/project/service_config.go:isConditionTrue;
	// extensions cannot import that unexported core helper.
	switch condition {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes":
		return true, nil
	default:
		return false, nil
	}
}

func dependencyConditionValue(value *structpb.Value) (string, error) {
	if value == nil {
		return "", nil
	}
	switch kind := value.Kind.(type) {
	case *structpb.Value_StringValue:
		return kind.StringValue, nil
	case *structpb.Value_BoolValue:
		return strconv.FormatBool(kind.BoolValue), nil
	case *structpb.Value_NumberValue:
		return strconv.FormatFloat(kind.NumberValue, 'g', -1, 64), nil
	case *structpb.Value_NullValue:
		return "", nil
	default:
		return "", fmt.Errorf("condition must be a string, boolean, or number")
	}
}

func (p *AgentServiceTargetProvider) dependencyEnvValue(name string) string {
	if value, ok := p.dependencyEnv[name]; ok {
		return value
	}
	return os.Getenv(name)
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
	agentEndpointKey := fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey)

	// Voice agents (kind: prompt-voice) use the base ENDPOINT as their callable
	// endpoint and deploy completion marker, and unified deploys also record
	// VERSION. Gate the base-endpoint path on the service's actual declared
	// kind (resolved via the shared agentkind lookup, so this agrees with the
	// deploy path and next-step reader) rather than on the env-var shape: a hosted
	// agent whose deploy partially failed (or whose vars were cleaned up) can also
	// present an empty VERSION with a lingering ENDPOINT, and for that case we
	// must still surface the actionable CodeMissingAgentEnvVars error below.
	// Kind resolution is best-effort here:
	// an error (or non-voice result) simply falls through to the hosted guard, so
	// hosted services keep their prior behavior on a path that never resolved
	// config before.
	// Endpoints may run in a fresh CLI process (e.g. `azd show`) where
	// ensureDeployContext has not populated p.projectPath or p.agentDefinitionPath.
	// A voice manifest supplied via a root `$ref` or an on-disk agent.yaml can only
	// be classified with the project root, and an explicit AGENT_DEFINITION_PATH
	// override drives deploy, so honor both here to match the deploy classification.
	// Both are resolved best-effort: any failure falls through to the hosted guard
	// below, so hosted behavior is unchanged.
	projectRoot := p.projectPath
	if projectRoot == "" {
		if proj, perr := p.azdClient.Project().Get(ctx, nil); perr == nil {
			projectRoot = proj.Project.Path
		}
	}
	agentDefinitionPath := p.agentDefinitionPath
	if agentDefinitionPath == "" {
		agentDefinitionPath = os.Getenv("AGENT_DEFINITION_PATH")
	}
	if isVoice, err := agentkind.IsPromptVoice(
		serviceConfig, projectRoot, agentDefinitionPath,
	); err == nil && isVoice && azdEnv[agentEndpointKey] != "" {
		return []string{azdEnv[agentEndpointKey]}, nil
	}

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
	p.adoptServiceConfig(serviceConfig)
	if err := p.ensureDeployContext(ctx); err != nil {
		return nil, err
	}
	serviceConfig = p.serviceConfig
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
	p.adoptServiceConfig(serviceConfig)
	if err := p.ensureDeployContext(ctx); err != nil {
		return nil, err
	}
	serviceConfig = p.serviceConfig
	agentDef, isContainerAgent, err := p.loadContainerAgentDefinition()
	if err != nil {
		return nil, err
	}
	if !isContainerAgent {
		return &azdext.ServicePackageResult{}, nil
	}
	if err := validateRegistryConnectionDefinition(agentDef); err != nil {
		return nil, err
	}

	// Code deploy takes precedence over stale or mixed image configuration.
	if agentDef.CodeConfiguration != nil {
		progress("Packaging code")
		zipPath, sha256Hex, err := p.packageCodeDeploy(ctx, serviceConfig)
		if err != nil {
			return nil, exterrors.InternalFromError(err, exterrors.OpContainerPackage, "code packaging failed")
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

	// Core image passthrough owns the artifact lifecycle for all pre-built images,
	// whether the source registry is public or accessed through a Foundry connection.
	if DockerImagePassthrough(serviceConfig.GetDocker()) {
		progress("Packaging pre-built container image")
		artifacts, err := p.packageContainer(ctx, serviceConfig, serviceContext)
		if err != nil {
			return nil, err
		}
		return &azdext.ServicePackageResult{Artifacts: artifacts}, nil
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
			buildRequest := &azdext.ContainerBuildRequest{
				ServiceName:    serviceConfig.Name,
				ServiceContext: serviceContext,
			}
			buildResponse, err := p.azdClient.
				Container().
				Build(ctx, buildRequest)
			if err != nil {
				return nil, exterrors.FromHost(err, exterrors.OpContainerBuild, "container build failed")
			}

			serviceContext.Build = append(serviceContext.Build, buildResponse.Result.Artifacts...)
		}

		artifacts, err := p.packageContainer(ctx, serviceConfig, serviceContext)
		if err != nil {
			return nil, err
		}

		newArtifacts = append(newArtifacts, artifacts...)
	}

	return &azdext.ServicePackageResult{
		Artifacts: newArtifacts,
	}, nil
}

func (p *AgentServiceTargetProvider) packageContainer(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
) ([]*azdext.Artifact, error) {
	packageResponse, err := p.azdClient.Container().Package(ctx, &azdext.ContainerPackageRequest{
		ServiceName:    serviceConfig.Name,
		ServiceContext: serviceContext,
	})
	if err != nil {
		return nil, exterrors.FromHost(err, exterrors.OpContainerPackage, "container package failed")
	}

	return packageResponse.Result.Artifacts, nil
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
	p.adoptServiceConfig(serviceConfig)
	if err := p.ensureDeployContext(ctx); err != nil {
		return nil, err
	}
	serviceConfig = p.serviceConfig

	agentDef, isContainerAgent, err := p.loadContainerAgentDefinition()
	if err != nil {
		return nil, err
	}
	if !isContainerAgent || agentDef.CodeConfiguration != nil {
		return &azdext.ServicePublishResult{}, nil
	}

	// A pre-built image does not start a container publish operation. Preserve
	// this fast path; Activity Bot selection still runs in Deploy because the
	// deployed agent identity is required to prefer an already-bound bot.
	if preBuiltArtifact := findPreBuiltImageArtifact(serviceContext.Package); preBuiltArtifact != nil {
		progress("Using pre-built container image, skipping publish")
		return &azdext.ServicePublishResult{
			Artifacts: []*azdext.Artifact{preBuiltArtifact},
		}, nil
	}

	progress("Publishing container")
	publishRequest := &azdext.ContainerPublishRequest{
		ServiceName:    serviceConfig.Name,
		ServiceContext: serviceContext,
	}
	publishResponse, err := p.azdClient.
		Container().
		Publish(ctx, publishRequest)

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

	return exterrors.FromHost(err, exterrors.OpContainerPublish, "container publish failed")
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
	if ca, isHosted, found, source, err :=
		AgentDefinitionFromResolvedService(
			p.serviceConfig,
			p.projectPath,
		); found || err != nil {
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

func (p *AgentServiceTargetProvider) resolveActivityBotName(
	ctx context.Context,
	botFinder interface {
		FindByMsaAppID(context.Context, string) (*botservice.BotReference, error)
	},
	serviceName string,
	agentName string,
	agentIdentityClientID string,
	defaultResourceGroup string,
	azdEnv map[string]string,
) (string, string, error) {
	if botFinder != nil && strings.TrimSpace(agentIdentityClientID) != "" {
		boundBot, err := botFinder.FindByMsaAppID(ctx, agentIdentityClientID)
		if err != nil {
			if _, ok := errors.AsType[*botservice.MultipleBotsForMsaAppIDError](err); ok {
				return "", "", classifyActivityBotLookupError(err)
			}
			fmt.Fprintf(
				os.Stderr,
				"Unable to search for an Azure Bot already bound to the deployed agent identity: %v\n",
				err,
			)
		}
		if boundBot != nil && strings.TrimSpace(boundBot.Name) != "" {
			fmt.Fprintf(
				os.Stderr,
				"Using Azure Bot already bound to the deployed agent identity: %q (resource group: %q)\n",
				boundBot.Name,
				boundBot.ResourceGroup,
			)
			return boundBot.Name, strings.TrimSpace(boundBot.ResourceGroup), nil
		}
	}

	key := envkey.AgentBotName(serviceName)
	name := strings.TrimSpace(azdEnv[key])
	if name == "" {
		resourceGroup := strings.TrimSpace(azdEnv["AZURE_RESOURCE_GROUP"])
		if resourceGroup == "" {
			resourceGroup = strings.TrimSpace(defaultResourceGroup)
		}
		name = botservice.BotName(agentName, botservice.BotScopeSalt(azdEnv["AZURE_SUBSCRIPTION_ID"], resourceGroup))
		fmt.Fprintf(
			os.Stderr,
			"Azure Bot name was not set in %s; using scope-qualified default %q. Set %s explicitly to use a custom bot name.\n",
			key,
			name,
			key,
		)
	} else {
		fmt.Fprintf(
			os.Stderr,
			"Using Azure Bot name from environment key %s: %q\n",
			key,
			name,
		)
	}

	if name == "" {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"Azure Bot name is required for Activity agents",
			"provide a Bot name and retry the deployment",
		)
	}

	return name, "", nil
}

// cSpell:ignore msaappid msaapp
func isMsaAppIDAlreadyInUseError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "msaappid is already in use") ||
		strings.Contains(msg, "msaapp id is already in use")
}

func classifyActivityBotError(err error, msaAppID string) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*botservice.TeamsChannelError](err); ok {
		return exterrors.ServiceFromAzure(err, exterrors.OpEnsureTeamsChannel)
	}
	if isMsaAppIDAlreadyInUseError(err) {
		return exterrors.Service(
			exterrors.OpEnsureActivityBot,
			exterrors.CodeMsaAppIDAlreadyInUse,
			fmt.Sprintf("Azure Bot MsaAppID %q is already in use", msaAppID),
			"botservice",
			"configure the Activity Bot name to use the existing Azure Bot bound to this MsaAppID, "+
				"or remove that Bot, then retry",
		)
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpEnsureActivityBot)
}

func classifyActivityBotLookupError(err error) error {
	if _, ok := errors.AsType[*botservice.MultipleBotsForMsaAppIDError](err); ok {
		return exterrors.Service(
			exterrors.OpGetActivityBot,
			exterrors.CodeMultipleBotsForMsaAppID,
			err.Error(),
			"",
			"keep only one Azure Bot bound to this MsaAppID, then retry",
		)
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpGetActivityBot)
}

// Deploy performs the deployment operation for the agent service
func (p *AgentServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	p.adoptServiceConfig(serviceConfig)
	if err := p.ensureDeployContext(ctx); err != nil {
		return nil, err
	}
	serviceConfig = p.serviceConfig

	voiceAgent, isVoice, err := resolveVoiceAgentForDeploy(
		p.agentDefinitionPath, serviceConfig, p.projectPath,
	)
	if err != nil {
		return nil, err
	}

	var agentDef agent_yaml.ContainerAgent
	if !isVoice {
		var isContainerAgent bool
		agentDef, isContainerAgent, err = p.loadContainerAgentDefinition()
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

		if err := validateEnvironmentVariableNames(
			serviceConfig.GetEnvironment(),
			agentDef.EnvironmentVariables,
		); err != nil {
			return nil, err
		}
		if err := validateRegistryConnectionDefinition(agentDef); err != nil {
			return nil, err
		}
	}

	// Ensure Foundry project is loaded
	progress("Loading Foundry project")
	if err := p.ensureFoundryProject(ctx); err != nil {
		return nil, err
	}

	// Get environment variables from azd
	progress("Loading deployment environment")
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
	p.dependencyEnv = azdEnv

	activityBotName := ""
	activityBotResourceGroup := ""
	activityBotOwned := false

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
	activityProfile, err := ResolveActivityProfileWithSettings(agentDef, serviceTargetConfig.Activity)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("invalid Activity configuration: %s", err),
			"check the activity configuration in azure.yaml",
		)
	}

	if warning := digitalWorkerBotTransitionWarning(serviceConfig.Name, activityProfile, azdEnv); warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}
	progress("Validating service dependencies")
	if err := validateRegistryConnectionDependency(
		ctx, serviceConfig, agentDef.RegistryConnectionID, p.projectServices, p.dependencyEnabled,
	); err != nil {
		return nil, err
	}
	if err := validateFoundryDependencies(
		ctx, serviceConfig, serviceTargetConfig, p.projectServices, azdEnv, p.dependencyEnabled,
	); err != nil {
		return nil, err
	}

	warnDeprecatedScaleSettings(ServiceConfigProps(serviceConfig))

	// Provision any declared Foundry memory stores before deploying the agent, since
	// the agent's memory_search tool depends on the store existing at runtime.
	if err := p.provisionMemoryStores(
		ctx, serviceTargetConfig, azdEnv["FOUNDRY_PROJECT_ENDPOINT"], progress,
	); err != nil {
		return nil, err
	}

	// Voice agents (kind: prompt-voice) use a different data-plane contract than
	// hosted/workflow agents. Resolve the definition first — honoring the
	// AGENT_DEFINITION_PATH override precedence so an override drives this dispatch
	// just as it does the container path — and route voice to an isolated method so
	// the container deploy path below stays byte-for-byte unchanged.
	if isVoice {
		return p.deployVoiceAgent(ctx, serviceConfig, voiceAgent, azdEnv, progress)
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
		progress("Agent version created; waiting for activation")
		projectEndpoint := azdEnv["FOUNDRY_PROJECT_ENDPOINT"]
		agentClient := agent_api.NewAgentClient(
			projectEndpoint,
			p.credential,
		)
		polledVersion, pollErr := p.waitForAgentActive(
			ctx,
			agentClient,
			endpointHost(projectEndpoint),
			result.agentName,
			result.agentVersion.Version,
			progress,
		)
		if pollErr != nil {
			return nil, pollErr
		}
		result.agentVersion = polledVersion
	} else {
		fmt.Fprintf(os.Stderr, "Agent version %s is already active.\n", result.agentVersion.Version)
	}

	// Patch agent-level endpoint/card fields
	if result.request.AgentEndpoint != nil || result.request.AgentCard != nil {
		progress("Updating agent endpoint settings")
	}
	if err := p.patchAgentEndpointFields(
		ctx, result.agentName, result.request.AgentEndpoint, result.request.AgentCard, azdEnv,
	); err != nil {
		return nil, err
	}

	if activityProfile.IsActivity && activityProfile.UseCase == ActivityUseCaseSimple {
		identity := result.agentVersion.InstanceIdentity
		if identity == nil || identity.ClientID == "" {
			return nil, exterrors.Dependency(
				exterrors.CodeAgentCreateFailed,
				"Activity agent deployment did not return an instance identity",
				"wait for the agent version to become active and retry",
			)
		}
		client, err := botservice.NewClient(azdEnv["AZURE_SUBSCRIPTION_ID"], p.credential, nil)
		if err != nil {
			return nil, err
		}
		progress("Resolving Activity bot configuration")
		activityBotName, activityBotResourceGroup, err = p.resolveActivityBotName(
			ctx,
			client,
			serviceConfig.Name,
			agentDef.Name,
			identity.ClientID,
			p.foundryProject.ResourceGroupName,
			azdEnv,
		)
		if err != nil {
			return nil, err
		}
		botResourceGroup := activityBotResourceGroup
		if strings.TrimSpace(botResourceGroup) == "" {
			botResourceGroup = p.foundryProject.ResourceGroupName
		}
		activityBotResourceGroup = botResourceGroup
		existingBot, err := client.GetBot(ctx, botResourceGroup, activityBotName)
		if err != nil {
			return nil, exterrors.ServiceFromAzure(err, exterrors.OpGetActivityBot)
		}
		var existingTags map[string]*string
		if existingBot != nil {
			existingTags = existingBot.Tags
		}
		var botTags map[string]*string
		activityBotOwned, botTags = activityBotOwnership(
			existingBot != nil,
			existingTags,
		)
		progress("Ensuring Azure Bot resource")
		ensureCfg := botservice.BotConfig{
			ResourceGroup:     botResourceGroup,
			BotName:           activityBotName,
			MsaAppID:          identity.ClientID,
			TenantID:          p.tenantId,
			MessagingEndpoint: botservice.MessagingEndpoint(azdEnv["FOUNDRY_PROJECT_ENDPOINT"], result.agentName),
			DisplayName:       result.agentName,
			Tags:              botTags,
		}
		if err := client.EnsureBot(ctx, ensureCfg); err != nil {
			// Recovery path: BotService enforces MsaAppID uniqueness. If a different
			// bot name is already bound to this identity, switch to that bot and retry.
			if isMsaAppIDAlreadyInUseError(err) {
				boundBot, findErr := client.FindByMsaAppID(ctx, identity.ClientID)
				if findErr != nil {
					return nil, classifyActivityBotLookupError(findErr)
				}
				if boundBot != nil && strings.TrimSpace(boundBot.Name) != "" {
					activityBotName = strings.TrimSpace(boundBot.Name)
					if strings.TrimSpace(boundBot.ResourceGroup) != "" {
						botResourceGroup = strings.TrimSpace(boundBot.ResourceGroup)
						activityBotResourceGroup = botResourceGroup
					}
					fmt.Fprintf(
						os.Stderr,
						"Azure Bot name %q conflicts for MsaAppID; reusing already-bound bot %q (resource group: %q).\n",
						ensureCfg.BotName,
						activityBotName,
						botResourceGroup,
					)
					ensureCfg.BotName = activityBotName
					ensureCfg.ResourceGroup = botResourceGroup
					existingBot, getErr := client.GetBot(ctx, botResourceGroup, activityBotName)
					if getErr != nil {
						return nil, exterrors.ServiceFromAzure(getErr, exterrors.OpGetActivityBot)
					}
					var existingTags map[string]*string
					if existingBot != nil {
						existingTags = existingBot.Tags
					}
					activityBotOwned, ensureCfg.Tags = activityBotOwnership(
						existingBot != nil,
						existingTags,
					)
					if retryErr := client.EnsureBot(ctx, ensureCfg); retryErr == nil {
						err = nil
					} else {
						return nil, classifyActivityBotError(retryErr, identity.ClientID)
					}
				}
			}
			if err != nil {
				return nil, classifyActivityBotError(err, identity.ClientID)
			}
		}
	}

	return p.finalizeDeploy(
		ctx,
		progress,
		serviceConfig,
		azdEnv,
		result.agentVersion,
		result.protocols,
		activityBotName,
		activityBotResourceGroup,
		activityBotOwned,
		activityProfile,
		serviceTargetConfig.Activity,
	)
}

func activityBotOwnership(
	botExists bool,
	existingTags map[string]*string,
) (bool, map[string]*string) {
	tags := maps.Clone(existingTags)
	if !botExists {
		if tags == nil {
			tags = make(map[string]*string)
		}
		tags[botservice.OwnershipTag] = new(botservice.OwnershipTagValue)
		return true, tags
	}
	value := tags[botservice.OwnershipTag]
	return value != nil && strings.EqualFold(*value, botservice.OwnershipTagValue), tags
}

func digitalWorkerBotTransitionWarning(
	serviceName string,
	activityProfile ActivityProfile,
	azdEnv map[string]string,
) string {
	if activityProfile.UseCase != ActivityUseCaseDigitalWorker {
		return ""
	}
	botName := strings.TrimSpace(azdEnv[envkey.AgentBotName(serviceName)])
	resourceGroup := strings.TrimSpace(azdEnv[envkey.AgentBotResourceGroup(serviceName)])
	owned := strings.EqualFold(strings.TrimSpace(azdEnv[envkey.AgentBotOwned(serviceName)]), "true")
	if botName == "" || resourceGroup == "" || !owned {
		return ""
	}
	return fmt.Sprintf(
		"Warning: service %q changed to digital_worker and still has the azd-managed Azure Bot %q "+
			"in resource group %q. Digital Worker deployment does not use this Bot; review it and "+
			"delete the legacy Bot manually if it is no longer needed.",
		serviceName,
		botName,
		resourceGroup,
	)
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

func validateRegistryConnectionDefinition(agentDef agent_yaml.ContainerAgent) error {
	rawConnectionRef := agentDef.RegistryConnectionID
	connectionRef := strings.TrimSpace(rawConnectionRef)
	if rawConnectionRef == "" {
		return nil
	}
	if connectionRef == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"registryConnectionId cannot be empty or whitespace",
			"set registryConnectionId to a Foundry project connection name or ID, or remove it",
		)
	}
	if agentDef.CodeConfiguration != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"registryConnectionId cannot be used with codeConfiguration",
			"use registryConnectionId with a pre-built image or remove it for code deploy",
		)
	}
	image := strings.TrimSpace(agentDef.Image)
	if image == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"registryConnectionId requires a pre-built container image",
			"set image on the azure.ai.agent service or remove registryConnectionId",
		)
	}
	if !containerref.IsFullyQualified(image) {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"registryConnectionId requires an image with an explicit registry host and repository",
			"set image to a fully qualified reference such as registry.example.com/team/agent:v1",
		)
	}
	return nil
}

// shouldUsePreBuiltImage determines whether to use a pre-built image.
//
// Behavior:
//   - A registry connection requires an image and always selects that pre-built image.
//   - If no image is configured in the loaded agent definition, always build from Dockerfile.
//     The image usually comes from the azure.yaml service image field, but can come from
//     a legacy agent.yaml fallback.
//   - In non-interactive mode (--no-prompt), the prompt returns the default
//     selection (index 0 = build from Dockerfile) automatically.
//   - In interactive mode, prompt the user. The default is to build, so users
//     who happen to have an image configured are not silently switched onto
//     the pre-built path.
func (p *AgentServiceTargetProvider) shouldUsePreBuiltImage(
	ctx context.Context,
	agentDef agent_yaml.ContainerAgent,
) (bool, error) {
	imageURL := strings.TrimSpace(agentDef.Image)
	if agentDef.RegistryConnectionID != "" {
		if err := validateRegistryConnectionDefinition(agentDef); err != nil {
			return false, err
		}
		log.Printf("registryConnectionId is configured: using pre-built image from agent definition")
		return true, nil
	}
	if imageURL == "" {
		return false, nil
	}

	// Releases before docker.imagePassthrough represented init --image with a
	// top-level image plus AZD_AGENT_SKIP_ACR=true. The docker property may be
	// absent, so preserve that environment marker as the legacy compatibility
	// contract. New projects use docker.imagePassthrough and do not enter this fallback.
	if !DockerImagePassthrough(p.serviceConfig.GetDocker()) &&
		p.shouldSkipACRForEnvironment(ctx) {
		log.Printf("legacy pre-built image configuration detected: using configured image")
		return true, nil
	}

	// Default to build so the legacy pre-built path requires an explicit choice.
	// New projects use docker.imagePassthrough and do not enter this fallback.
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

	// Seed core-expanded values before resolving legacy variables.
	resolvedEnvVars := maps.Clone(serviceConfig.GetEnvironment())
	if resolvedEnvVars == nil {
		resolvedEnvVars = make(map[string]string)
	}
	if agentDef.EnvironmentVariables != nil {
		for _, envVar := range *agentDef.EnvironmentVariables {
			if _, found := resolvedEnvVars[envVar.Name]; found {
				continue
			}
			resolvedEnvVars[envVar.Name] = p.resolveEnvironmentVariables(
				envVar.Name,
				envVar.Value,
				serviceConfig.GetEnvironment(),
				azdEnv,
			)
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
	WarnOrphanedConfigEnv(serviceConfig)

	var cpu, memory string
	if foundryAgentConfig != nil && foundryAgentConfig.Container != nil && foundryAgentConfig.Container.Resources != nil {
		cpu = foundryAgentConfig.Container.Resources.Cpu
		memory = foundryAgentConfig.Container.Resources.Memory
	}
	// $ref services never persist resolved defaults to azure.yaml,
	// so deploy applies the extension defaults here to keep $ref
	// and inline services consistent.
	if cpu == "" {
		cpu = DefaultCpu
	}
	if memory == "" {
		memory = DefaultMemory
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
			{Protocol: string(agent_api.AgentProtocolResponses), Version: "2.0.0"},
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
		return exterrors.ServiceFromAzure(err, exterrors.OpUpdateAgent)
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
	activityBotName string,
	activityBotResourceGroup string,
	activityBotOwned bool,
	activityProfile ActivityProfile,
	activitySettings *ActivitySettings,
) (*azdext.ServiceDeployResult, error) {
	progress("Registering agent environment variables")

	err := p.registerAgentEnvironmentVariables(
		ctx,
		azdEnv,
		serviceConfig,
		agentVersion,
		protocols,
		activityBotName,
		activityBotResourceGroup,
		activityBotOwned,
		activityProfile,
		activitySettings,
	)
	if err != nil {
		return nil, err
	}

	artifacts := p.deployArtifacts(
		agentVersion.Name,
		agentVersion.Version,
		azdEnv["AZURE_AI_PROJECT_ID"],
		azdEnv["FOUNDRY_PROJECT_ENDPOINT"],
		activityProfile,
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
				"either set 'image' on the azure.yaml agent service, "+
					"or run 'azd package' and 'azd publish' to build from a Dockerfile",
			)
		}
	}

	progress("Preparing hosted agent configuration")
	prep, err := p.prepareDeploy(serviceConfig, agentDef, azdEnv, []agent_yaml.AgentBuildOption{
		agent_yaml.WithImageURL(fullImageURL),
	})
	if err != nil {
		return nil, err
	}

	// Display agent information
	p.displayAgentInfo(prep.request)

	// Create agent
	progress("Submitting agent version creation request")
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

// voiceOverriddenHostEnvKey optionally routes prompt voice agent calls directly
// to a regional data-plane host (bypassing the public Foundry APIM when needed).
// When unset, default endpoint routing is used.
//
//nolint:gosec // env var key name, not a credential
const voiceOverriddenHostEnvKey = "AZURE_VOICE_OVERRIDDEN_HOST"

// deployVoiceAgent deploys a declarative (managed) voice agent (kind:
// prompt-voice) to the Foundry service through the unified /agents API. This
// method is intentionally isolated from the container deploy path so the two
// contracts never entangle.
func (p *AgentServiceTargetProvider) deployVoiceAgent(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	va agent_yaml.VoiceAgent,
	azdEnv map[string]string,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	progress("Deploying voice agent")

	request, err := agent_yaml.CreateVoiceAgentAPIRequest(va)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("invalid voice agent definition: %s", err),
			"fix the agent definition in azure.yaml and re-run `azd deploy`",
		)
	}

	projectEndpoint := azdEnv["FOUNDRY_PROJECT_ENDPOINT"]
	if projectEndpoint == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"cannot deploy voice agent: the Foundry project endpoint is not set",
			"run 'azd provision' or connect to an existing project via "+
				"'azd ai agent init --project-id <resource-id>'",
		)
	}

	agentClient := agent_api.NewAgentClient(projectEndpoint, p.credential)

	serviceKey := p.getServiceKey(serviceConfig.Name)
	agentObject, deployOp, err := p.deployVoiceAgentRemote(
		ctx, agentClient, request, azdEnv, progress,
	)
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, deployOp)
	}
	if err := validateVoiceAgentDeployResponse(agentObject); err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "Voice agent '%s' deployed successfully!\n", agentObject.Name)

	// Persist NAME first and ENDPOINT last. ENDPOINT is used as the voice deploy
	// completion marker by other commands, so avoid writing it before NAME.
	baseEndpoint := buildVoiceWSProtocolURL(projectEndpoint, agentObject.Name)
	versionKey := fmt.Sprintf("AGENT_%s_VERSION", serviceKey)
	versionValue := agentObject.Versions.Latest.Version
	endpointKey := fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey)
	if _, setErr := p.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: p.env.Name,
		Key:     endpointKey,
		Value:   "",
	}); setErr != nil {
		return nil, fmt.Errorf("clearing voice agent environment variable %s: %w", endpointKey, setErr)
	}
	for _, envVar := range []struct{ key, value string }{
		{fmt.Sprintf("AGENT_%s_NAME", serviceKey), agentObject.Name},
		{versionKey, versionValue},
		{fmt.Sprintf("AGENT_%s_PROJECT_ENDPOINT", serviceKey), strings.TrimRight(projectEndpoint, "/")},
		{endpointKey, baseEndpoint},
	} {
		if _, setErr := p.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: p.env.Name,
			Key:     envVar.key,
			Value:   envVar.value,
		}); setErr != nil {
			return nil, fmt.Errorf("registering voice agent environment variable %s: %w", envVar.key, setErr)
		}
	}

	artifacts := []*azdext.Artifact{{
		Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Location:     baseEndpoint,
		LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
		Metadata: map[string]string{
			"agentName": agentObject.Name,
			"label":     "Voice agent endpoint",
			"clickable": "false",
		},
	}}

	return &azdext.ServiceDeployResult{Artifacts: artifacts}, nil
}

func validateVoiceAgentDeployResponse(agentObject *agent_api.AgentObject) error {
	if agentObject == nil {
		return fmt.Errorf("malformed voice agent service response: missing agent object")
	}
	if strings.TrimSpace(agentObject.Name) == "" {
		return fmt.Errorf("malformed voice agent service response: missing agent name")
	}
	if strings.TrimSpace(agentObject.Versions.Latest.Version) == "" {
		return fmt.Errorf("malformed voice agent service response: missing latest agent version")
	}
	return nil
}

func (p *AgentServiceTargetProvider) deployVoiceAgentRemote(
	ctx context.Context,
	agentClient *agent_api.AgentClient,
	request *agent_api.CreateAgentRequest,
	azdEnv map[string]string,
	progress azdext.ProgressReporter,
) (*agent_api.AgentObject, string, error) {
	overriddenHost := azdEnv[voiceOverriddenHostEnvKey]
	remoteAgent, getErr := agentClient.GetVoiceAgent(
		ctx, request.Name, agent_api.AgentEndpointAPIVersion, overriddenHost,
	)
	shouldUpdate, decisionErr := shouldUpdateVoiceAgent(remoteAgent, getErr)
	if decisionErr != nil {
		return nil, exterrors.OpGetAgent, decisionErr
	}
	if shouldUpdate {
		progress("Updating voice agent using unified API")
		updateRequest := &agent_api.UpdateAgentRequest{
			CreateAgentVersionRequest: request.CreateAgentVersionRequest,
		}
		agentObject, err := agentClient.UpdateVoiceAgent(
			ctx, request.Name, updateRequest, agent_api.AgentEndpointAPIVersion, overriddenHost,
		)
		return agentObject, exterrors.OpUpdateAgent, err
	}

	progress("Creating voice agent using unified API")
	agentObject, err := agentClient.CreateVoiceAgent(ctx, request, agent_api.AgentEndpointAPIVersion, overriddenHost)
	return agentObject, exterrors.OpCreateAgent, err
}

func shouldUpdateVoiceAgent(remoteAgent *agent_api.AgentObject, getErr error) (bool, error) {
	if getErr == nil {
		return remoteAgent != nil, nil
	}
	if respErr, ok := errors.AsType[*azcore.ResponseError](getErr); ok && respErr.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, getErr
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

	progress("Loading code package artifact")
	zipData, err := os.ReadFile(zipPath) //nolint:gosec // zipPath comes from the artifact location set during packaging
	if err != nil {
		return nil, fmt.Errorf("failed to read ZIP artifact: %w", err)
	}
	// Clean up temp file
	defer os.Remove(zipPath)

	progress("Preparing code agent configuration")
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
	progress("Checking existing agent")
	_, getErr := agentClient.GetAgent(ctx, agentDef.Name, agent_api.AgentEndpointAPIVersion)
	var agentResp *agent_api.AgentObject

	if getErr != nil {
		// Only fall back to create on 404; classify every other service response.
		if respErr, ok := errors.AsType[*azcore.ResponseError](getErr); !ok || respErr.StatusCode != http.StatusNotFound {
			return nil, exterrors.ServiceFromAzure(getErr, exterrors.OpCreateAgent)
		}
		// Agent doesn't exist — create
		progress("Creating new agent from code package")
		fmt.Fprintf(os.Stderr, "Creating new agent: %s\n", agentDef.Name)
		agentResp, err = agentClient.CreateAgentFromZip(
			ctx, agentDef.Name, versionRequest, zipData, sha256Hex, agent_api.AgentEndpointAPIVersion,
		)
		if err != nil {
			return nil, exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
		}
	} else {
		// Agent exists — update
		progress("Updating existing agent from code package")
		writeExistingAgentVersionWarning(agentDef.Name)
		agentResp, err = agentClient.UpdateAgentFromZip(
			ctx, agentDef.Name, versionRequest, zipData, sha256Hex, agent_api.AgentEndpointAPIVersion,
		)
		if err != nil {
			return nil, exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
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
	activityProfile ActivityProfile,
	protocols []agent_yaml.ProtocolVersionRecord,
) []*azdext.Artifact {
	artifacts := []*azdext.Artifact{}

	// Add playground URL only for non-Activity agents.
	if !activityProfile.IsActivity && projectResourceID != "" {
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
	serviceName string,
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
	var lastPollErr error

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
			lastPollErr = err
			fmt.Fprintf(os.Stderr, "  Warning: poll failed: %s\n", err)
			// Reset counters on error — don't count transient failures
			consecutiveActive = 0
			consecutiveFailed = 0
			continue
		}
		lastPollErr = nil
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
				return nil, agentDeploymentFailedError(versionResp, serviceName)
			}
			fmt.Fprintf(os.Stderr, "  Status: failed (confirming...)\n")
		default:
			consecutiveActive = 0
			consecutiveFailed = 0
			fmt.Fprintf(os.Stderr, "  Status: %s...\n", versionResp.Status)
		}
	}

	// Timeout
	if lastPollErr != nil {
		return nil, exterrors.ServiceFromAzure(lastPollErr, exterrors.OpCreateAgent)
	}
	lastStatus := "unknown"
	if lastVersion != nil {
		lastStatus = lastVersion.Status
	}
	return nil, exterrors.Service(
		exterrors.OpCreateAgent,
		"timeout",
		fmt.Sprintf("agent deployment timed out (last status: %s); check agent status manually", lastStatus),
		serviceName,
		"run `azd ai agent show` to inspect the latest deployment status",
	)
}

func agentDeploymentFailedError(versionResp *agent_api.AgentVersionObject, serviceName string) error {
	code := "failed"
	errMsg := "agent deployment failed"
	suggestion := "run `azd ai agent show` to inspect the latest deployment status"
	if versionResp.Error != nil {
		code = versionResp.Error.Code
		errMsg = fmt.Sprintf("agent deployment failed: [%s] %s", code, versionResp.Error.Message)
		if remediation, ok := nextstep.RemediationForUserErrorCode(nextstep.UserErrorCode(code)); ok {
			suggestion = fmt.Sprintf("run `%s` to %s", remediation.Command, remediation.Description)
		}
	}
	if versionResp.RequestID != "" {
		errMsg += fmt.Sprintf(" (request-id: %s)", versionResp.RequestID)
	}

	return exterrors.Service(exterrors.OpCreateAgent, code, errMsg, serviceName, suggestion)
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
	activityBotName string,
	activityBotResourceGroup string,
	activityBotOwned bool,
	activityProfile ActivityProfile,
	activitySettings *ActivitySettings,
) error {
	if agentVersionResponse.Name == "" {
		return fmt.Errorf("agent name is empty; cannot register environment variables")
	}
	if agentVersionResponse.Version == "" {
		return fmt.Errorf("agent version is empty; cannot register environment variables")
	}

	serviceKey := p.getServiceKey(serviceConfig.Name)
	versionKey := fmt.Sprintf("AGENT_%s_VERSION", serviceKey)
	identityClientID := ""
	identityPrincipalID := ""
	if agentVersionResponse.InstanceIdentity != nil {
		identityClientID = strings.TrimSpace(agentVersionResponse.InstanceIdentity.ClientID)
		identityPrincipalID = strings.TrimSpace(agentVersionResponse.InstanceIdentity.PrincipalID)
	}
	envVars := []azdext.SetEnvRequest{
		{EnvName: p.env.Name, Key: versionKey, Value: ""},
		{EnvName: p.env.Name, Key: fmt.Sprintf("AGENT_%s_NAME", serviceKey), Value: agentVersionResponse.Name},
		{EnvName: p.env.Name, Key: envkey.AgentInstanceIdentityClientID(serviceConfig.Name), Value: identityClientID},
		{EnvName: p.env.Name, Key: envkey.AgentInstanceIdentityPrincipalID(serviceConfig.Name), Value: identityPrincipalID},
	}

	// Set the base agent endpoint used for session management (not protocol-specific).
	baseEndpointKey := fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey)
	projectEndpoint := strings.TrimRight(azdEnv["FOUNDRY_PROJECT_ENDPOINT"], "/")
	envVars = append(envVars, azdext.SetEnvRequest{EnvName: p.env.Name, Key: baseEndpointKey, Value: fmt.Sprintf(
		"%s/agents/%s/versions/%s", projectEndpoint, agentVersionResponse.Name, agentVersionResponse.Version,
	)})

	endpoints := agentInvocationEndpoints(
		azdEnv["FOUNDRY_PROJECT_ENDPOINT"],
		agentVersionResponse.Name,
		protocols,
	)
	for _, ep := range endpoints {
		suffix := strings.ToUpper(ep.Protocol)
		key := fmt.Sprintf("AGENT_%s_%s_ENDPOINT", serviceKey, suffix)
		envVars = append(envVars, azdext.SetEnvRequest{EnvName: p.env.Name, Key: key, Value: ep.URL})
	}
	envVars = append(envVars,
		azdext.SetEnvRequest{EnvName: p.env.Name, Key: envkey.AgentProjectEndpoint(serviceConfig.Name), Value: projectEndpoint},
		azdext.SetEnvRequest{EnvName: p.env.Name, Key: versionKey, Value: agentVersionResponse.Version},
	)
	if activityBotName != "" {
		envVars = append(envVars,
			azdext.SetEnvRequest{
				EnvName: p.env.Name,
				Key:     envkey.AgentBotName(serviceConfig.Name),
				Value:   activityBotName,
			},
			azdext.SetEnvRequest{
				EnvName: p.env.Name,
				Key:     envkey.AgentBotResourceGroup(serviceConfig.Name),
				Value:   activityBotResourceGroup,
			},
			azdext.SetEnvRequest{
				EnvName: p.env.Name,
				Key:     envkey.AgentBotOwned(serviceConfig.Name),
				Value:   strconv.FormatBool(activityBotOwned),
			},
		)
	}
	if activityProfile.UseCase == ActivityUseCaseDigitalWorker {
		if activitySettings == nil || activitySettings.Publish == nil {
			return fmt.Errorf("Digital Worker publish configuration is missing")
		}
		blueprint := agentVersionResponse.Blueprint
		if blueprint == nil || strings.TrimSpace(blueprint.ClientID) == "" {
			return fmt.Errorf("Digital Worker agent version is missing Blueprint client ID")
		}

		envVars = append(envVars, azdext.SetEnvRequest{
			EnvName: p.env.Name,
			Key:     envkey.AgentBlueprintClientID(serviceConfig.Name),
			Value:   blueprint.ClientID,
		})
	}

	for i := range envVars {
		_, err := p.azdClient.Environment().SetValue(ctx, &envVars[i])
		if err != nil {
			return fmt.Errorf("failed to set environment variable %s: %w", envVars[i].Key, err)
		}
	}

	return nil
}

// resolveEnvironmentVariables expands legacy inline templates.
func (p *AgentServiceTargetProvider) resolveEnvironmentVariables(
	name string,
	value string,
	serviceEnvironment map[string]string,
	azdEnv map[string]string,
) string {
	resolved, err := ResolveAgentEnvironmentVariable(
		name,
		value,
		serviceEnvironment,
		func(varName string) string {
			return azdEnv[varName]
		},
	)
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
