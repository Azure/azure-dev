// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// AgentDeployChange describes one configuration difference in a dry run.
type AgentDeployChange struct {
	Group  string `json:"group"`
	Field  string `json:"field"`
	Change string `json:"change"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// AgentDeployArtifactPlan describes the artifact work a deployment would perform.
type AgentDeployArtifactPlan struct {
	Type        string `json:"type"`
	Image       string `json:"image,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	WouldBuild  bool   `json:"wouldBuild"`
	WouldPush   bool   `json:"wouldPush"`
	WouldUpload bool   `json:"wouldUpload"`
}

// AgentDeployPlan is the stable output of an Agent deployment dry run.
type AgentDeployPlan struct {
	Agent            string                  `json:"agent"`
	Service          string                  `json:"service,omitempty"`
	Source           string                  `json:"source"`
	Action           string                  `json:"action"`
	RemoteComparison string                  `json:"remoteComparison"`
	Changes          []AgentDeployChange     `json:"changes"`
	Artifact         AgentDeployArtifactPlan `json:"artifact"`
}

// AgentLookup retrieves an Agent without mutating it.
type AgentLookup func(context.Context, string, bool) (*agent_api.AgentObject, error)

// AgentDeployPlanOptions configures an Agent deployment dry run.
type AgentDeployPlanOptions struct {
	ServiceConfig   *azdext.ServiceConfig
	ProjectRoot     string
	DefinitionPath  string
	CodePath        string
	ProjectEndpoint string
	Environment     map[string]string
	Lookup          AgentLookup
}

// PlanAgentDeploy validates local deployment inputs and compares them with the
// latest deployed Agent version when a Foundry project endpoint is available.
// It does not perform any remote mutation.
func PlanAgentDeploy(ctx context.Context, options AgentDeployPlanOptions) (*AgentDeployPlan, error) {
	agentDef, source, serviceName, codePath, inputEnvironment, err := resolveDeployPlanInput(options)
	if err != nil {
		return nil, err
	}

	resolvedEnv, err := resolveDeployPlanEnvironment(
		agentDef,
		options.ServiceConfig,
		options.Environment,
		inputEnvironment,
	)
	if err != nil {
		return nil, err
	}

	cpu, memory := deployPlanResources(agentDef)
	buildOptions := []agent_yaml.AgentBuildOption{
		agent_yaml.WithEnvironmentVariables(resolvedEnv),
		agent_yaml.WithCPU(cpu),
		agent_yaml.WithMemory(memory),
	}

	artifact := AgentDeployArtifactPlan{Type: "containerImage"}
	if agentDef.CodeConfiguration != nil {
		artifact.Type = "codePackage"
		artifact.WouldUpload = true
		zipPath, sha256Hex, packageErr := packageDeployPlanCode(ctx, codePath, agentDef)
		if packageErr != nil {
			return nil, packageErr
		}
		if removeErr := removeDeployPlanPackage(zipPath); removeErr != nil {
			return nil, removeErr
		}
		artifact.SHA256 = sha256Hex
	} else if strings.TrimSpace(agentDef.Image) != "" &&
		options.ServiceConfig != nil &&
		options.ServiceConfig.GetDocker().GetImagePassthrough() {
		artifact.Image = redactDeployPlanImage(agentDef.Image)
		buildOptions = append(buildOptions, agent_yaml.WithImageURL(agentDef.Image))
	} else {
		artifact.WouldBuild = true
		artifact.WouldPush = true
		artifact.Image = redactDeployPlanImage(agentDef.Image)
		// The final image reference is assigned by the container publish phase.
		buildOptions = append(buildOptions, agent_yaml.WithImageURL("<built-container-image>"))
	}

	request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(agentDef, buildOptions...)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentRequest,
			fmt.Sprintf("failed to create agent request from definition: %s", err),
			"fix the agent definition and retry",
		)
	}
	applyAgentMetadata(request)

	plan := &AgentDeployPlan{
		Agent:            request.Name,
		Service:          serviceName,
		Source:           source,
		Action:           "create",
		RemoteComparison: "unavailableUntilProvision",
		Changes:          desiredStateChanges(request, artifact),
		Artifact:         artifact,
	}

	projectEndpoint := strings.TrimRight(strings.TrimSpace(options.ProjectEndpoint), "/")
	if projectEndpoint == "" {
		return plan, nil
	}

	lookup := options.Lookup
	if lookup == nil {
		credential, credentialErr := azidentity.NewAzureDeveloperCLICredential(
			&azidentity.AzureDeveloperCLICredentialOptions{},
		)
		if credentialErr != nil {
			return nil, exterrors.Auth(
				exterrors.CodeCredentialCreationFailed,
				fmt.Sprintf("failed to create Azure credential: %s", credentialErr),
				"run 'azd auth login' to authenticate",
			)
		}
		client := agent_api.NewAgentClient(projectEndpoint, credential)
		lookup = func(ctx context.Context, name string, includeDigitalWorkerType bool) (*agent_api.AgentObject, error) {
			return client.GetAgent(
				ctx,
				name,
				agent_api.AgentEndpointAPIVersion,
				includeDigitalWorkerType,
			)
		}
	}

	current, err := lookup(ctx, request.Name, ResolveActivityProfile(agentDef).IsActivity)
	if err != nil {
		if responseError, ok := errors.AsType[*azcore.ResponseError](err); ok &&
			responseError.StatusCode == http.StatusNotFound {
			plan.RemoteComparison = "notFound"
			return plan, nil
		}
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpGetAgent)
	}

	plan.Action = "createVersion"
	plan.RemoteComparison = "compared"
	plan.Changes, err = compareAgentDeployState(request, &current.Versions.Latest, artifact)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func resolveDeployPlanInput(
	options AgentDeployPlanOptions,
) (agent_yaml.ContainerAgent, string, string, string, map[string]string, error) {
	if options.ServiceConfig == nil {
		definitionPath := strings.TrimSpace(options.DefinitionPath)
		if definitionPath == "" {
			definitionPath = "agent.yaml"
		}
		agentDef, environment, err := prepareDeployPlanStandaloneDefinition(definitionPath, options.Environment)
		if err != nil {
			return agent_yaml.ContainerAgent{}, "", "", "", nil, err
		}

		codePath := strings.TrimSpace(options.CodePath)
		if codePath == "" {
			codePath = filepath.Dir(definitionPath)
		}
		codePath, err = filepath.Abs(codePath)
		if err != nil {
			return agent_yaml.ContainerAgent{}, "", "", "", nil, exterrors.Validation(
				exterrors.CodeInvalidFilePath,
				fmt.Sprintf("invalid agent code path %q: %s", options.CodePath, err),
				"pass a valid source directory with --code",
			)
		}
		return agentDef, filepath.Base(definitionPath), "", codePath, environment, nil
	}

	if err := ResolveServiceConfigInPlace(options.ServiceConfig, options.ProjectRoot); err != nil {
		return agent_yaml.ContainerAgent{}, "", "", "", nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("failed to resolve service config for %s: %s", options.ServiceConfig.Name, err),
			"fix the agent service configuration in azure.yaml",
		)
	}
	agentDef, isHosted, source, err := LoadAgentDefinition(options.ServiceConfig, options.ProjectRoot)
	if err != nil {
		return agent_yaml.ContainerAgent{}, "", "", "", nil, err
	}
	if !isHosted {
		return agent_yaml.ContainerAgent{}, "", "", "", nil, exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			"agent deploy --dry-run currently supports hosted agents only",
			"select an azure.ai.agent service with kind: hosted",
		)
	}
	if source.IsLegacy() {
		WarnLegacyAgentShape(source)
	}

	codePath := strings.TrimSpace(options.CodePath)
	if codePath == "" {
		codePath, err = paths.JoinAllowRoot(options.ProjectRoot, options.ServiceConfig.GetRelativePath())
		if err != nil {
			return agent_yaml.ContainerAgent{}, "", "", "", nil, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("invalid service path for %s: %s", options.ServiceConfig.Name, err),
				"update azure.yaml so the agent service path stays within the project directory",
			)
		}
	} else {
		codePath, err = filepath.Abs(codePath)
		if err != nil {
			return agent_yaml.ContainerAgent{}, "", "", "", nil, exterrors.Validation(
				exterrors.CodeInvalidFilePath,
				fmt.Sprintf("invalid agent code path %q: %s", options.CodePath, err),
				"pass a valid source directory with --code",
			)
		}
	}
	sourceName := "azure.yaml"
	if source == AgentDefinitionSourceDisk {
		sourceName = "agent.yaml"
	}
	return agentDef, sourceName, options.ServiceConfig.Name, codePath, nil, nil
}

func prepareDeployPlanStandaloneDefinition(
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
			"agent deploy --dry-run currently supports hosted agents only",
			"use a hosted agent definition or preview a hosted service from azure.yaml",
		)
	}
	if agentDefinition.CodeConfiguration == nil && agentDefinition.Image == "" {
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
	return agentDefinition, environment, nil
}

func packageDeployPlanCode(
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

func resolveDeployPlanEnvironment(
	agentDef agent_yaml.ContainerAgent,
	serviceConfig *azdext.ServiceConfig,
	azdEnv map[string]string,
	inputEnvironment map[string]string,
) (map[string]string, error) {
	var serviceEnv map[string]string
	if serviceConfig != nil {
		serviceEnv = serviceConfig.GetEnvironment()
	}
	resolved := maps.Clone(inputEnvironment)
	if resolved == nil {
		resolved = map[string]string{}
	}
	maps.Copy(resolved, serviceEnv)
	if agentDef.EnvironmentVariables == nil {
		return resolved, nil
	}
	for _, variable := range *agentDef.EnvironmentVariables {
		if _, found := resolved[variable.Name]; found {
			continue
		}
		value, err := ResolveAgentEnvironmentVariable(
			variable.Name,
			variable.Value,
			serviceEnv,
			func(name string) string { return azdEnv[name] },
		)
		if err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("failed to resolve environment variable %s: %s", variable.Name, err),
				"fix the environment-variable expression in the agent definition",
			)
		}
		resolved[variable.Name] = value
	}
	return resolved, nil
}

func deployPlanResources(agentDef agent_yaml.ContainerAgent) (string, string) {
	cpu, memory := DefaultCpu, DefaultMemory
	if agentDef.Resources != nil {
		if agentDef.Resources.Cpu != "" {
			cpu = agentDef.Resources.Cpu
		}
		if agentDef.Resources.Memory != "" {
			memory = agentDef.Resources.Memory
		}
	}
	return cpu, memory
}

func desiredStateChanges(
	request *agent_api.CreateAgentRequest,
	artifact AgentDeployArtifactPlan,
) []AgentDeployChange {
	desired, err := hostedDefinition(request.Definition)
	if err != nil {
		return nil
	}
	changes := []AgentDeployChange{{
		Group: "metadata", Field: "name", Change: "add", After: request.Name,
	}}
	if request.Description != nil {
		changes = append(changes, AgentDeployChange{
			Group: "metadata", Field: "description", Change: "add", After: *request.Description,
		})
	}
	if len(request.Metadata) > 0 {
		changes = append(changes, AgentDeployChange{
			Group: "metadata", Field: "metadata", Change: "add", After: request.Metadata,
		})
	}
	changes = append(changes,
		AgentDeployChange{
			Group: "protocols", Field: "protocols", Change: "add",
			After: sortedProtocols(desired.ProtocolVersions),
		},
		AgentDeployChange{Group: "resources", Field: "cpu", Change: "add", After: desired.CPU},
		AgentDeployChange{Group: "resources", Field: "memory", Change: "add", After: desired.Memory},
	)
	changes = append(changes, createEnvironmentChanges(desired.EnvironmentVariables)...)
	changes = append(changes, AgentDeployChange{
		Group: "artifact", Field: "artifact", Change: "add", After: artifact,
	})
	return changes
}

func compareAgentDeployState(
	desiredRequest *agent_api.CreateAgentRequest,
	current *agent_api.AgentVersionObject,
	artifact AgentDeployArtifactPlan,
) ([]AgentDeployChange, error) {
	desired, err := hostedDefinition(desiredRequest.Definition)
	if err != nil {
		return nil, fmt.Errorf("decode desired hosted agent definition: %w", err)
	}
	existing, err := hostedDefinition(current.Definition)
	if err != nil {
		return nil, fmt.Errorf("decode deployed hosted agent definition: %w", err)
	}

	changes := []AgentDeployChange{}
	changes = appendValueChange(changes, "metadata", "description", pointerString(current.Description), pointerString(desiredRequest.Description))
	changes = appendValueChange(changes, "metadata", "metadata", current.Metadata, desiredRequest.Metadata)
	changes = appendValueChange(
		changes, "protocols", "protocols",
		sortedProtocols(existing.ProtocolVersions),
		sortedProtocols(desired.ProtocolVersions),
	)
	changes = appendValueChange(changes, "resources", "cpu", existing.CPU, desired.CPU)
	changes = appendValueChange(changes, "resources", "memory", existing.Memory, desired.Memory)
	changes = append(changes, compareEnvironment(existing.EnvironmentVariables, desired.EnvironmentVariables)...)
	changes = appendValueChange(
		changes,
		"artifact",
		"containerImage",
		redactDeployPlanImage(containerImage(existing)),
		redactDeployPlanImage(containerImage(desired)),
	)
	changes = appendValueChange(
		changes,
		"configuration",
		"codeConfiguration",
		existing.CodeConfiguration,
		desired.CodeConfiguration,
	)
	changes = appendValueChange(
		changes,
		"configuration",
		"sessionConfiguration",
		existing.SessionConfiguration,
		desired.SessionConfiguration,
	)
	if artifact.Type == "codePackage" {
		changes = append(changes, AgentDeployChange{
			Group: "artifact", Field: "codePackage", Change: "update",
			Before: "deployed package", After: artifact.SHA256,
		})
	} else if artifact.WouldBuild {
		changes = append(changes, AgentDeployChange{
			Group: "artifact", Field: "containerImage", Change: "update",
			Before: redactDeployPlanImage(containerImage(existing)), After: "image built from local source",
		})
	}

	return changes, nil
}

func hostedDefinition(value any) (agent_api.HostedAgentDefinition, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return agent_api.HostedAgentDefinition{}, err
	}
	var definition agent_api.HostedAgentDefinition
	if err := json.Unmarshal(data, &definition); err != nil {
		return agent_api.HostedAgentDefinition{}, err
	}
	return definition, nil
}

func appendValueChange(
	changes []AgentDeployChange,
	group string,
	field string,
	before any,
	after any,
) []AgentDeployChange {
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) == string(afterJSON) {
		return changes
	}
	change := "update"
	if isEmptyJSON(beforeJSON) {
		change = "add"
	} else if isEmptyJSON(afterJSON) {
		change = "remove"
	}
	return append(changes, AgentDeployChange{
		Group: group, Field: field, Change: change, Before: before, After: after,
	})
}

func isEmptyJSON(value []byte) bool {
	return string(value) == "null" || string(value) == `""` ||
		string(value) == "{}" || string(value) == "[]"
}

func createEnvironmentChanges(environment map[string]string) []AgentDeployChange {
	var changes []AgentDeployChange
	for _, name := range slices.Sorted(maps.Keys(environment)) {
		if name == "AZURE_AI_MODEL_DEPLOYMENT_NAME" {
			changes = append(changes, AgentDeployChange{
				Group: "modelDeployment", Field: name, Change: "add", After: environment[name],
			})
			continue
		}
		changes = append(changes, AgentDeployChange{
			Group: "environment", Field: name, Change: "add", After: "<redacted>",
		})
	}
	return changes
}

func compareEnvironment(before, after map[string]string) []AgentDeployChange {
	names := map[string]struct{}{}
	for name := range before {
		names[name] = struct{}{}
	}
	for name := range after {
		names[name] = struct{}{}
	}

	var changes []AgentDeployChange
	for _, name := range slices.Sorted(maps.Keys(names)) {
		if before[name] == after[name] {
			continue
		}
		group := "environment"
		beforeValue, afterValue := any("<redacted>"), any("<redacted>")
		if name == "AZURE_AI_MODEL_DEPLOYMENT_NAME" {
			group = "modelDeployment"
			beforeValue, afterValue = before[name], after[name]
		}
		change := "update"
		if _, found := before[name]; !found {
			change, beforeValue = "add", nil
		} else if _, found := after[name]; !found {
			change, afterValue = "remove", nil
		}
		changes = append(changes, AgentDeployChange{
			Group: group, Field: name, Change: change, Before: beforeValue, After: afterValue,
		})
	}
	return changes
}

func sortedProtocols(protocols []agent_api.ProtocolVersionRecord) []agent_api.ProtocolVersionRecord {
	if len(protocols) == 0 {
		protocols = []agent_api.ProtocolVersionRecord{{
			Protocol: agent_api.AgentProtocolResponses,
			Version:  "2.0.0",
		}}
	}
	result := slices.Clone(protocols)
	slices.SortFunc(result, func(a, b agent_api.ProtocolVersionRecord) int {
		if protocolCmp := cmp.Compare(a.Protocol, b.Protocol); protocolCmp != 0 {
			return protocolCmp
		}
		return cmp.Compare(a.Version, b.Version)
	})
	return result
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func containerImage(definition agent_api.HostedAgentDefinition) string {
	if definition.ContainerConfiguration == nil {
		return ""
	}
	return definition.ContainerConfiguration.Image
}

func redactDeployPlanImage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		value, _, _ = strings.Cut(value, "?")
		value, _, _ = strings.Cut(value, "#")
		if at := strings.LastIndex(value, "@"); at >= 0 {
			firstSlash := strings.Index(value, "/")
			if (firstSlash == -1 || at < firstSlash) && strings.Contains(value[:at], ":") {
				return value[at+1:]
			}
		}
		return value
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

var removeDeployPlanPackage = func(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove temporary Agent package: %w", err)
	}
	return nil
}
