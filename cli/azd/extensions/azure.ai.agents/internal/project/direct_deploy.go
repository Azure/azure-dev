// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// DirectDeployOptions configures a standalone hosted-agent deployment.
type DirectDeployOptions struct {
	DefinitionPath  string
	CodePath        string
	ProjectEndpoint string
	Environment     map[string]string
	Progress        azdext.ProgressReporter
}

// DirectDeployResult describes the deployed hosted-agent version.
type DirectDeployResult struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	State    string `json:"state"`
	Endpoint string `json:"endpoint,omitempty"`
}

// PreparedStandaloneHostedAgent contains validated deployment inputs with no
// remote Agent or Toolbox mutations performed.
type PreparedStandaloneHostedAgent struct {
	definition      agent_yaml.ContainerAgent
	environment     map[string]string
	projectEndpoint string
	progress        azdext.ProgressReporter
	zipData         []byte
	sha256Hex       string
	credential      azcore.TokenCredential
}

// DeployStandaloneHostedAgent deploys a hosted agent directly from agent.yaml
// and source code without requiring an azd project or azure.yaml.
func DeployStandaloneHostedAgent(
	ctx context.Context,
	options DirectDeployOptions,
) (*DirectDeployResult, error) {
	prepared, err := PrepareStandaloneHostedAgent(ctx, options)
	if err != nil {
		return nil, err
	}
	return DeployPreparedStandaloneHostedAgent(ctx, prepared, nil)
}

// PrepareStandaloneHostedAgent validates and packages a standalone hosted
// Agent without performing any remote resource mutations.
func PrepareStandaloneHostedAgent(
	ctx context.Context,
	options DirectDeployOptions,
) (*PreparedStandaloneHostedAgent, error) {
	definitionPath := strings.TrimSpace(options.DefinitionPath)
	if definitionPath == "" {
		definitionPath = "agent.yaml"
	}
	projectEndpoint := strings.TrimRight(strings.TrimSpace(options.ProjectEndpoint), "/")
	if projectEndpoint == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"a Foundry project endpoint is required for agent deployment",
			"run 'azd ai project set <endpoint>' or pass --project-endpoint",
		)
	}

	agentDefinition, environment, err := prepareStandaloneHostedDefinition(
		definitionPath,
		options.Environment,
	)
	if err != nil {
		return nil, err
	}

	codePath := strings.TrimSpace(options.CodePath)
	if codePath == "" {
		codePath = filepath.Dir(definitionPath)
	}
	codePath, err = filepath.Abs(codePath)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidFilePath,
			fmt.Sprintf("invalid agent code path %q: %s", options.CodePath, err),
			"pass a valid source directory with --code",
		)
	}
	info, err := os.Stat(codePath)
	if err != nil || !info.IsDir() {
		return nil, exterrors.Dependency(
			exterrors.CodeInvalidFilePath,
			fmt.Sprintf("agent code directory %q was not found", codePath),
			"pass a readable source directory with --code",
		)
	}

	progress := options.Progress
	if progress == nil {
		progress = func(string) {}
	}
	progress("Packaging agent source code")
	zipPath, sha256Hex, err := packageStandaloneCode(ctx, codePath, agentDefinition)
	if err != nil {
		return nil, err
	}
	defer os.Remove(zipPath)
	// #nosec G304 -- zipPath is created by the package helper in the system temp directory.
	zipData, err := os.ReadFile(zipPath)
	if err != nil {
		return nil, fmt.Errorf("read agent code package: %w", err)
	}

	if _, err := standaloneAgentRequest(agentDefinition, environment); err != nil {
		return nil, err
	}

	credential, err := newStandaloneCredential()
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run 'azd auth login' to authenticate",
		)
	}

	return &PreparedStandaloneHostedAgent{
		definition:      agentDefinition,
		environment:     environment,
		projectEndpoint: projectEndpoint,
		progress:        progress,
		zipData:         zipData,
		sha256Hex:       sha256Hex,
		credential:      credential,
	}, nil
}

func standaloneAgentRequest(
	agentDefinition agent_yaml.ContainerAgent,
	environment map[string]string,
) (*agent_api.CreateAgentRequest, error) {
	request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(
		agentDefinition,
		agent_yaml.WithEnvironmentVariables(environment),
		agent_yaml.WithCPU(agentDefinition.Resources.Cpu),
		agent_yaml.WithMemory(agentDefinition.Resources.Memory),
	)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentRequest,
			fmt.Sprintf("failed to create agent request from definition: %s", err),
			"fix the agent.yaml definition and retry",
		)
	}
	applyAgentMetadata(request)
	return request, nil
}

// DeployPreparedStandaloneHostedAgent deploys previously validated Agent inputs.
// Environment overrides, such as a deployed Toolbox endpoint, take precedence.
func DeployPreparedStandaloneHostedAgent(
	ctx context.Context,
	prepared *PreparedStandaloneHostedAgent,
	environmentOverrides map[string]string,
) (*DirectDeployResult, error) {
	if prepared == nil {
		return nil, fmt.Errorf("prepared standalone hosted agent must not be nil")
	}

	environment := maps.Clone(prepared.environment)
	if environment == nil {
		environment = map[string]string{}
	}
	maps.Copy(environment, environmentOverrides)
	request, err := standaloneAgentRequest(prepared.definition, environment)
	if err != nil {
		return nil, err
	}

	agentClient := agent_api.NewAgentClient(prepared.projectEndpoint, prepared.credential)
	versionRequest := &agent_api.CreateAgentVersionRequest{
		Description: request.Description,
		Metadata:    request.Metadata,
		Definition:  request.Definition,
	}

	prepared.progress("Checking existing agent")
	localProfile := ResolveActivityProfile(prepared.definition)
	existingAgent, getErr := agentClient.GetAgent(
		ctx,
		prepared.definition.Name,
		agent_api.AgentEndpointAPIVersion,
		true,
	)
	var agentObject *agent_api.AgentObject
	if getErr != nil {
		if responseError, ok := errors.AsType[*azcore.ResponseError](getErr); !ok ||
			responseError.StatusCode != http.StatusNotFound {
			return nil, exterrors.ServiceFromAzure(getErr, exterrors.OpCreateAgent)
		}
		prepared.progress("Creating agent from code package")
		agentObject, err = agentClient.CreateAgentFromZip(
			ctx,
			prepared.definition.Name,
			versionRequest,
			prepared.zipData,
			prepared.sha256Hex,
			agent_api.AgentEndpointAPIVersion,
		)
	} else {
		if err := reconcileStandaloneEndpointWithDeployedAgent(request, localProfile, existingAgent); err != nil {
			return nil, err
		}
		prepared.progress("Creating a new agent version from code package")
		writeExistingAgentVersionWarning(prepared.definition.Name)
		agentObject, err = agentClient.UpdateAgentFromZip(
			ctx,
			prepared.definition.Name,
			versionRequest,
			prepared.zipData,
			prepared.sha256Hex,
			agent_api.AgentEndpointAPIVersion,
		)
	}
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
	}

	version := &agentObject.Versions.Latest
	provider := &AgentServiceTargetProvider{credential: prepared.credential}
	if version.Status != "active" {
		version, err = provider.waitForAgentActive(
			ctx,
			agentClient,
			endpointHost(prepared.projectEndpoint),
			prepared.definition.Name,
			version.Version,
			prepared.progress,
		)
		if err != nil {
			return nil, err
		}
	}
	if err := provider.patchAgentEndpointFields(
		ctx,
		prepared.definition.Name,
		request.AgentEndpoint,
		request.AgentCard,
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": prepared.projectEndpoint},
	); err != nil {
		return nil, err
	}

	protocols := prepared.definition.Protocols
	if len(protocols) == 0 {
		protocols = []agent_yaml.ProtocolVersionRecord{{Protocol: "responses", Version: "2.0.0"}}
	}
	endpoints := agentInvocationEndpoints(prepared.projectEndpoint, prepared.definition.Name, protocols)
	endpoint := ""
	if len(endpoints) > 0 {
		endpoint = endpoints[0].URL
	}

	return &DirectDeployResult{
		Name:     prepared.definition.Name,
		Version:  version.Version,
		State:    version.Status,
		Endpoint: endpoint,
	}, nil
}

func reconcileStandaloneEndpointWithDeployedAgent(
	request *agent_api.CreateAgentRequest,
	localProfile ActivityProfile,
	existingAgent *agent_api.AgentObject,
) error {
	resolvedProfile, err := ResolveDeployedActivityProfile(
		localProfile,
		existingAgent.DigitalWorkerType,
	)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			err.Error(),
			digitalWorkerTypeMismatchSuggestion(),
		)
	}

	if resolvedProfile.IsActivity && request.AgentEndpoint == nil {
		request.AgentEndpoint = &agent_api.AgentEndpoint{}
	}
	EnsureActivityEndpointAuthSchemeForProfile(request.AgentEndpoint, resolvedProfile)
	return nil
}

func newStandaloneCredential() (azcore.TokenCredential, error) {
	azureCLI, azureCLIError := azidentity.NewAzureCLICredential(
		&azidentity.AzureCLICredentialOptions{AdditionallyAllowedTenants: []string{"*"}},
	)
	azd, azdError := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{AdditionallyAllowedTenants: []string{"*"}},
	)
	credentials := make([]azcore.TokenCredential, 0, 2)
	if azureCLIError == nil {
		credentials = append(credentials, azureCLI)
	}
	if azdError == nil {
		credentials = append(credentials, azd)
	}
	if len(credentials) == 0 {
		return nil, fmt.Errorf("Azure CLI credential: %w; azd credential: %w", azureCLIError, azdError)
	}
	if len(credentials) == 1 {
		return credentials[0], nil
	}
	credential, err := azidentity.NewChainedTokenCredential(credentials, nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure credential chain: %w", err)
	}
	return credential, nil
}

func prepareStandaloneHostedDefinition(
	path string,
	overrides map[string]string,
) (agent_yaml.ContainerAgent, map[string]string, error) {
	// #nosec G304 -- reading the user-selected definition is the purpose of this operation.
	data, err := os.ReadFile(path)
	if err != nil {
		return agent_yaml.ContainerAgent{}, nil, exterrors.Dependency(
			exterrors.CodeAgentDefinitionNotFound,
			fmt.Sprintf("failed to read agent definition %q: %s", path, err),
			"run the command from the agent directory or pass an explicit agent.yaml path",
		)
	}
	agentDefinition, isHosted, err := parseContainerAgentYAML(data)
	if err != nil {
		return agent_yaml.ContainerAgent{}, nil, err
	}
	if !isHosted {
		return agent_yaml.ContainerAgent{}, nil, exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			"standalone deploy currently supports hosted agents only",
			"use kind: hosted or deploy the declarative agent after its service contract is available",
		)
	}
	if agentDefinition.Image != "" {
		return agent_yaml.ContainerAgent{}, nil, exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			"standalone deploy currently supports source-code deployment only",
			"remove 'image' and deploy source code, or compose the agent into azure.yaml for image deployment",
		)
	}
	if agentDefinition.CodeConfiguration == nil {
		language := strings.ToLower(strings.TrimSpace(agentDefinition.Language))
		if language == "" {
			language = "python"
		}
		if language != "python" {
			return agent_yaml.ContainerAgent{}, nil, exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("language %q requires an explicit code_configuration", language),
				"set code_configuration.runtime and code_configuration.entry_point in agent.yaml",
			)
		}
		agentDefinition.Language = language
		agentDefinition.CodeConfiguration = &agent_yaml.CodeConfiguration{
			Runtime:    "python_3_13",
			EntryPoint: "main.py",
		}
	}
	if agentDefinition.Resources == nil {
		agentDefinition.Resources = &agent_yaml.ContainerResources{Cpu: DefaultCpu, Memory: DefaultMemory}
	} else {
		if agentDefinition.Resources.Cpu == "" {
			agentDefinition.Resources.Cpu = DefaultCpu
		}
		if agentDefinition.Resources.Memory == "" {
			agentDefinition.Resources.Memory = DefaultMemory
		}
	}

	environment := maps.Clone(overrides)
	if environment == nil {
		environment = map[string]string{}
	}
	if agentDefinition.EnvironmentVariables != nil {
		for _, variable := range *agentDefinition.EnvironmentVariables {
			if _, overridden := environment[variable.Name]; overridden {
				continue
			}
			resolved, resolveErr := ResolveAgentEnvironmentVariable(
				variable.Name,
				variable.Value,
				nil,
				os.Getenv,
			)
			if resolveErr != nil {
				return agent_yaml.ContainerAgent{}, nil, exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					fmt.Sprintf("failed to resolve environment variable %s: %s", variable.Name, resolveErr),
					"fix the environment-variable expression in agent.yaml",
				)
			}
			environment[variable.Name] = resolved
		}
	}
	if agentDefinition.Toolbox != nil {
		environment["TOOLBOX_NAME"] = strings.TrimSpace(agentDefinition.Toolbox.Name)
		if version := strings.TrimSpace(agentDefinition.Toolbox.Version); version != "" {
			environment["TOOLBOX_VERSION"] = version
		}
	}

	return agentDefinition, environment, nil
}

func packageStandaloneCode(
	ctx context.Context,
	codePath string,
	agentDefinition agent_yaml.ContainerAgent,
) (string, string, error) {
	dependencyResolution := agent_yaml.DefaultDependencyResolution
	if agentDefinition.CodeConfiguration.DependencyResolution != nil &&
		strings.TrimSpace(*agentDefinition.CodeConfiguration.DependencyResolution) != "" {
		dependencyResolution = *agentDefinition.CodeConfiguration.DependencyResolution
	}
	if dependencyResolution != "bundled" {
		return zipSourceDir(ctx, codePath)
	}
	if strings.HasPrefix(agentDefinition.CodeConfiguration.Runtime, "dotnet_") {
		return (&AgentServiceTargetProvider{}).packageDotnetBundled(codePath)
	}
	if strings.HasPrefix(agentDefinition.CodeConfiguration.Runtime, "python_") {
		if err := validatePythonBundledDeps(codePath); err != nil {
			return "", "", err
		}
	}
	return zipSourceDir(ctx, codePath)
}
