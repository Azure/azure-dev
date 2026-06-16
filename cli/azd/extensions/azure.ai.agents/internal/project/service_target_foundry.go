// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Ensure FoundryServiceTargetProvider implements the ServiceTargetProvider interface.
var _ azdext.ServiceTargetProvider = &FoundryServiceTargetProvider{}

// FoundryServiceTargetProvider implements the service target for `host: microsoft.foundry`.
//
// A single service stands for a whole Foundry project. This foundation supports a
// single hosted agent end-to-end (prebuilt image or code-deploy runtime) by mapping
// the inline agent definition onto the existing agent deploy machinery. Multi-agent
// fan-out, container (docker) builds, and data-plane reconcile (deployments,
// connections, toolboxes, skills, routines) are intentionally out of scope here and
// land in follow-up work (design spec #8590 §2.6, §2.8).
type FoundryServiceTargetProvider struct {
	azdClient *azdext.AzdClient

	// agent is an AgentServiceTargetProvider reused for shared auth setup and the
	// low-level create/poll/finalize helpers, since the deploy primitives are
	// identical to the azure.ai.agent host.
	agent *AgentServiceTargetProvider

	config        *FoundryProjectConfig
	hostedAgent   FoundryAgent
	projectRoot   string
	serviceConfig *azdext.ServiceConfig
	initialized   bool
}

// NewFoundryServiceTargetProvider creates a new FoundryServiceTargetProvider instance.
func NewFoundryServiceTargetProvider(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &FoundryServiceTargetProvider{
		azdClient: azdClient,
		agent:     &AgentServiceTargetProvider{azdClient: azdClient},
	}
}

// Initialize binds the inline Foundry configuration, validates the single supported
// hosted agent, resolves the project root, and sets up authentication.
func (p *FoundryServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	if p.initialized {
		return nil
	}

	p.serviceConfig = serviceConfig
	p.agent.serviceConfig = serviceConfig

	// The Foundry keys are top-level service properties carried on
	// AdditionalProperties (not under `config:`), per design spec §2.1.
	var config *FoundryProjectConfig
	if err := UnmarshalStruct(serviceConfig.AdditionalProperties, &config); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("failed to parse microsoft.foundry service config: %s", err),
			"check the Foundry service definition in azure.yaml",
		)
	}
	if config == nil {
		config = &FoundryProjectConfig{}
	}
	p.config = config

	hostedAgent, err := config.Validate()
	if err != nil {
		return err
	}
	p.hostedAgent = hostedAgent

	// Resolve the project root (the directory holding azure.yaml). Agent `project`
	// paths resolve relative to it.
	proj, err := p.azdClient.Project().Get(ctx, nil)
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			fmt.Sprintf("failed to get project: %s", err),
			"run 'azd init' to initialize your project",
		)
	}
	p.projectRoot = proj.Project.Path

	// Resolve environment, subscription, tenant, and credential (shared with the
	// azure.ai.agent host).
	if err := p.agent.setupAuth(ctx); err != nil {
		return err
	}

	// Connect to an existing project via `endpoint:` when no project was
	// provisioned, so deploy can run without `azd provision` (spec §1.4).
	if err := p.resolveProjectFromEndpoint(ctx); err != nil {
		return err
	}

	p.initialized = true
	return nil
}

// Endpoints returns the deployed agent's endpoints. Delegates to the shared
// implementation, which reads the per-service AGENT_<KEY>_* environment values
// written during Deploy.
func (p *FoundryServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return p.agent.Endpoints(ctx, serviceConfig, targetResource)
}

// GetTargetResource resolves the ARM resource for the Foundry project. Delegates to
// the shared implementation, which resolves the project from AZURE_AI_PROJECT_ID.
func (p *FoundryServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	return p.agent.GetTargetResource(ctx, subscriptionId, serviceConfig, defaultResolver)
}

// Package builds the deploy artifact for the single hosted agent. Code-deploy
// (runtime) agents are zipped from their `project` directory; prebuilt-image agents
// need no packaging.
func (p *FoundryServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	if p.hostedAgent.deployMode() != deployModeRuntime {
		progress("Using pre-built container image, skipping package")
		return &azdext.ServicePackageResult{}, nil
	}

	srcDir, err := paths.JoinAllowRoot(p.projectRoot, p.hostedAgent.Project)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("invalid agent project path %q: %s", p.hostedAgent.Project, err),
			"set 'project' to a directory within the project root",
		)
	}

	progress("Packaging code")
	zipPath, sha256Hex, err := zipSourceDir(ctx, srcDir)
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

// Publish is a no-op for the supported deploy modes: prebuilt images are already
// remote, and code-deploy uploads its ZIP during Deploy.
func (p *FoundryServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy posts the single hosted agent to Foundry via CreateAgentVersion (image) or
// a ZIP code deploy (runtime), then polls until the version is active and registers
// the agent's environment values.
func (p *FoundryServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	azdEnv, err := p.environmentValues(ctx)
	if err != nil {
		return nil, err
	}

	// An explicit endpoint: on the service points at an existing project; use it as
	// the deploy endpoint when provision did not write FOUNDRY_PROJECT_ENDPOINT.
	// Expand ${VAR} references so values like ${MY_ENDPOINT} resolve correctly.
	if azdEnv["FOUNDRY_PROJECT_ENDPOINT"] == "" && p.config.Endpoint != "" {
		endpoint := p.config.Endpoint
		if expanded, err := ExpandEnv(p.config.Endpoint, func(name string) string { return azdEnv[name] }); err == nil && expanded != "" {
			endpoint = expanded
		}
		azdEnv["FOUNDRY_PROJECT_ENDPOINT"] = endpoint
	}
	if azdEnv["FOUNDRY_PROJECT_ENDPOINT"] == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"FOUNDRY_PROJECT_ENDPOINT is required: environment variable was not found in the current azd environment",
			"run 'azd provision', or set 'endpoint:' on the microsoft.foundry service to use an existing project",
		)
	}

	// Reject an insecure or non-Foundry endpoint (http, foreign host, explicit
	// port, or a partially expanded ${VAR}) before using it to construct an
	// authenticated AgentClient.
	if _, err := validateFoundryEndpoint(azdEnv["FOUNDRY_PROJECT_ENDPOINT"]); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("FOUNDRY_PROJECT_ENDPOINT is not a valid Foundry project endpoint: %v", err),
			"set 'endpoint:' (or FOUNDRY_PROJECT_ENDPOINT) to an https Foundry project URL, "+
				"e.g. https://<account>.services.ai.azure.com/api/projects/<project>",
		)
	}

	request, protocols, err := p.buildAgentRequest(azdEnv)
	if err != nil {
		return nil, err
	}

	var agentVersion *agent_api.AgentVersionObject
	if p.hostedAgent.deployMode() == deployModeRuntime {
		agentVersion, err = p.deployCodeAgent(ctx, serviceContext, progress, request, azdEnv)
	} else {
		progress("Creating agent")
		agentVersion, err = p.agent.createAgent(ctx, request, azdEnv)
	}
	if err != nil {
		return nil, err
	}

	// Poll until the agent version is active.
	if agentVersion.Status != "active" {
		agentClient := agent_api.NewAgentClient(azdEnv["FOUNDRY_PROJECT_ENDPOINT"], p.agent.credential)
		polled, pollErr := p.agent.waitForAgentActive(ctx, agentClient, request.Name, agentVersion.Version, progress)
		if pollErr != nil {
			return nil, pollErr
		}
		agentVersion = polled
	} else {
		fmt.Fprintf(os.Stderr, "Agent version %s is already active.\n", agentVersion.Version)
	}

	return p.agent.finalizeDeploy(ctx, progress, serviceConfig, azdEnv, agentVersion, protocols)
}

// environmentValues returns the current azd environment values as a map.
func (p *FoundryServiceTargetProvider) environmentValues(ctx context.Context) (map[string]string, error) {
	resp, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: p.agent.env.Name,
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
	return azdEnv, nil
}

// buildAgentRequest maps the inline hosted agent onto a CreateAgentRequest, resolving
// env values and defaulting protocols. It returns the request plus the protocol list
// used for endpoint registration.
func (p *FoundryServiceTargetProvider) buildAgentRequest(
	azdEnv map[string]string,
) (*agent_api.CreateAgentRequest, []agent_yaml.ProtocolVersionRecord, error) {
	containerAgent, err := p.hostedAgent.toContainerAgent()
	if err != nil {
		return nil, nil, err
	}

	// Default to the "responses" protocol when none is specified.
	if len(containerAgent.Protocols) == 0 {
		containerAgent.Protocols = []agent_yaml.ProtocolVersionRecord{
			{Protocol: string(agent_api.AgentProtocolResponses), Version: "1.0.0"},
		}
	}

	options := []agent_yaml.AgentBuildOption{}
	if env := p.hostedAgent.resolvedEnv(azdEnv); len(env) > 0 {
		options = append(options, agent_yaml.WithEnvironmentVariables(env))
	}
	if p.hostedAgent.Container != nil && p.hostedAgent.Container.Resources != nil {
		if cpu := p.hostedAgent.Container.Resources.Cpu; cpu != "" {
			options = append(options, agent_yaml.WithCPU(cpu))
		}
		if memory := p.hostedAgent.Container.Resources.Memory; memory != "" {
			options = append(options, agent_yaml.WithMemory(memory))
		}
	}
	if p.hostedAgent.deployMode() == deployModeImage {
		options = append(options, agent_yaml.WithImageURL(p.hostedAgent.Image))
	}

	request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(containerAgent, options...)
	if err != nil {
		return nil, nil, exterrors.Validation(
			exterrors.CodeInvalidAgentRequest,
			fmt.Sprintf("failed to build agent request: %s", err),
			"verify the agent definition in azure.yaml",
		)
	}
	applyAgentMetadata(request)

	return request, containerAgent.Protocols, nil
}

// deployCodeAgent performs a ZIP code deploy for a runtime-mode hosted agent,
// creating the agent when absent and updating it (new version) when present.
func (p *FoundryServiceTargetProvider) deployCodeAgent(
	ctx context.Context,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
	request *agent_api.CreateAgentRequest,
	azdEnv map[string]string,
) (*agent_api.AgentVersionObject, error) {
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
	defer os.Remove(zipPath)

	versionRequest := &agent_api.CreateAgentVersionRequest{
		Description: request.Description,
		Metadata:    request.Metadata,
		Definition:  request.Definition,
	}

	agentClient := agent_api.NewAgentClient(azdEnv["FOUNDRY_PROJECT_ENDPOINT"], p.agent.credential)

	progress("Creating agent")
	_, getErr := agentClient.GetAgent(ctx, request.Name, agent_api.AgentEndpointAPIVersion)

	var agentResp *agent_api.AgentObject
	if getErr != nil {
		// Only fall back to create on 404; propagate other errors (auth, 5xx, network).
		if respErr, ok := errors.AsType[*azcore.ResponseError](getErr); !ok || respErr.StatusCode != http.StatusNotFound {
			return nil, fmt.Errorf("failed to check if agent exists: %w", getErr)
		}
		fmt.Fprintf(os.Stderr, "Creating new agent: %s\n", request.Name)
		agentResp, err = agentClient.CreateAgentFromZip(
			ctx, request.Name, versionRequest, zipData, sha256Hex, agent_api.AgentEndpointAPIVersion,
		)
		if err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeAgentCreateFailed,
				fmt.Sprintf("failed to create agent from ZIP: %s; check the agent definition and try again", err),
			)
		}
	} else {
		writeExistingAgentVersionWarning(request.Name)
		agentResp, err = agentClient.UpdateAgentFromZip(
			ctx, request.Name, versionRequest, zipData, sha256Hex, agent_api.AgentEndpointAPIVersion,
		)
		if err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeAgentCreateFailed,
				fmt.Sprintf("failed to update agent from ZIP: %s; check the agent definition and try again", err),
			)
		}
	}

	return &agentResp.Versions.Latest, nil
}
