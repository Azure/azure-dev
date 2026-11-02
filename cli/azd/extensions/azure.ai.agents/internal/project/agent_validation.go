// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// AgentDefinitionValidation describes the effective definition validated for a
// service.
type AgentDefinitionValidation struct {
	Kind   agent_yaml.AgentKind
	Name   string
	Source AgentDefinitionSource
}

// ValidateAgentServiceDefinition validates the effective definition for every
// supported azure.ai.agent kind.
func ValidateAgentServiceDefinition(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (AgentDefinitionValidation, error) {
	return validateAgentServiceDefinition(svc, projectRoot, true)
}

// ValidateAgentServiceDefinitionContent validates agent fields without
// resolving local Connection references.
func ValidateAgentServiceDefinitionContent(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (AgentDefinitionValidation, error) {
	return validateAgentServiceDefinition(svc, projectRoot, false)
}

func validateAgentServiceDefinition(
	svc *azdext.ServiceConfig,
	projectRoot string,
	validateConnections bool,
) (AgentDefinitionValidation, error) {
	if err := validateRuntimeAgentSources(svc); err != nil {
		return AgentDefinitionValidation{}, err
	}

	props := ServiceConfigProps(svc)
	if props == nil || len(props.GetFields()) == 0 {
		_, _, _, err := agentDefinitionFromDisk(svc, projectRoot)
		return AgentDefinitionValidation{}, err
	}

	resolved, err := resolveServiceProps(props, svc.GetName(), projectRoot)
	if err != nil {
		return AgentDefinitionValidation{}, invalidAgentDefinitionError(svc, err)
	}

	kind, name, err := validatedAgentIdentity(svc, resolved)
	if err != nil {
		return AgentDefinitionValidation{}, err
	}
	result := AgentDefinitionValidation{
		Kind:   kind,
		Name:   name,
		Source: AgentDefinitionSourceInline,
	}

	switch kind {
	case agent_yaml.AgentKindHosted:
		if err := validateHostedAgentContainerSettings(svc.GetName(), resolved); err != nil {
			return AgentDefinitionValidation{}, err
		}
		if err := validateHostedAgentServiceDefinition(svc, resolved, projectRoot); err != nil {
			return AgentDefinitionValidation{}, err
		}
	case agent_yaml.AgentKindPrompt:
		if err := validatePromptAgentServiceDefinition(
			svc,
			projectRoot,
			validateConnections,
		); err != nil {
			return AgentDefinitionValidation{}, err
		}
		if result.Name == "" {
			result.Name = svc.GetName()
		}
	case agent_yaml.AgentKindPromptVoice, agent_yaml.AgentKindVoice:
		if err := validateVoiceAgentServiceDefinition(svc, resolved, projectRoot); err != nil {
			return AgentDefinitionValidation{}, err
		}
	default:
		return AgentDefinitionValidation{}, unsupportedAgentKindError(svc, kind)
	}

	return result, nil
}

func validateHostedAgentContainerSettings(serviceName string, properties *structpb.Struct) error {
	if structKind(properties) != string(agent_yaml.AgentKindHosted) {
		return nil
	}
	container, declared := properties.GetFields()["container"]
	if !declared {
		return nil
	}
	schema, err := compiledAgentDefinitionSchema()
	if err != nil {
		return fmt.Errorf("load agent resource schema: %w", err)
	}
	containerSchema := schema.Properties["container"]
	if containerSchema == nil {
		return fmt.Errorf("embedded agent schema does not define container settings")
	}
	if err := containerSchema.Validate(container.AsInterface()); err != nil {
		validation, ok := errors.AsType[*jsonschema.ValidationError](err)
		if !ok {
			return fmt.Errorf("validate agent container settings: %w", err)
		}
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("service %q has invalid container settings: %s", serviceName, resourceValidationMessage(validation)),
			"fix container.resources in azure.yaml or its referenced file; use only cpu and memory with "+
				"non-empty string values in the documented formats, or omit the fields to use defaults",
		)
	}
	return nil
}

func resourceValidationMessage(validation *jsonschema.ValidationError) string {
	if len(validation.Causes) > 0 {
		messages := make([]string, 0, len(validation.Causes))
		for _, cause := range validation.Causes {
			messages = append(messages, resourceValidationMessage(cause))
		}
		return strings.Join(messages, "; ")
	}
	path := strings.Join(append([]string{"container"}, validation.InstanceLocation...), ".")
	switch constraint := validation.ErrorKind.(type) {
	case *kind.Type:
		return fmt.Sprintf("%s must be %s", path, strings.Join(constraint.Want, " or "))
	case *kind.Pattern:
		return fmt.Sprintf("%s must match pattern %q", path, constraint.Want)
	case *kind.AdditionalProperties:
		return fmt.Sprintf("%s contains unsupported fields %q", path, constraint.Properties)
	default:
		return fmt.Sprintf("%s violates %s", path, strings.Join(validation.ErrorKind.KeywordPath(), "."))
	}
}

func validatedAgentIdentity(
	svc *azdext.ServiceConfig,
	resolved *structpb.Struct,
) (agent_yaml.AgentKind, string, error) {
	kindValue, found := resolved.GetFields()["kind"]
	if !found {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent service %q requires a kind", svc.GetName()),
			"set kind to one of: "+validAgentKindsText(),
		)
	}
	if _, ok := kindValue.Kind.(*structpb.Value_StringValue); !ok ||
		strings.TrimSpace(kindValue.GetStringValue()) == "" {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent service %q kind must be a non-empty string", svc.GetName()),
			"set kind to one of: "+validAgentKindsText(),
		)
	}

	kind := agent_yaml.AgentKind(strings.TrimSpace(kindValue.GetStringValue()))
	name := strings.TrimSpace(resolved.GetFields()["name"].GetStringValue())
	return kind, name, nil
}

func validateHostedAgentServiceDefinition(
	svc *azdext.ServiceConfig,
	resolved *structpb.Struct,
	projectRoot string,
) error {
	hosted, isHosted, _, err := LoadHostedAgentDefinition(svc, projectRoot)
	if err != nil {
		return err
	}
	if !isHosted {
		return unsupportedAgentKindError(svc, agent_yaml.AgentKind(structKind(resolved)))
	}
	if err := validateEnvironmentVariableNames(
		svc.GetEnvironment(),
		hosted.EnvironmentVariables,
	); err != nil {
		return err
	}
	if err := validateRegistryConnectionDefinition(hosted); err != nil {
		return err
	}

	if err := validateAgentActivitySettings(svc, resolved, hosted); err != nil {
		return err
	}

	buildConfig := &agent_yaml.AgentBuildConfig{}
	if hosted.CodeConfiguration == nil {
		// Source-based hosted agents receive their image from the build stage.
		// Supply a valid placeholder so request mapping can validate authored
		// fields such as session configuration without requiring a build.
		buildConfig.ImageURL = "validation.invalid/agent:latest"
	}
	if _, err := agent_yaml.CreateHostedAgentAPIRequest(hosted, buildConfig); err != nil {
		return invalidAgentDefinitionError(svc, err)
	}
	return nil
}

func validatePromptAgentServiceDefinition(
	svc *azdext.ServiceConfig,
	projectRoot string,
	validateConnections bool,
) error {
	prompt, found, err := PromptAgentFromResolvedService(svc, projectRoot)
	if err != nil {
		return err
	}
	if !found {
		return unsupportedAgentKindError(svc, agent_yaml.AgentKindPrompt)
	}
	if err := applyPromptAgentServiceName(&prompt, svc.GetName()); err != nil {
		return err
	}
	if !validateConnections {
		prompt.Connections = nil
	}

	agentDir, err := promptAgentValidationDir(svc, projectRoot)
	if err != nil {
		return invalidAgentDefinitionError(svc, err)
	}
	graph, err := newPromptGraph(agentDir, &prompt, nil, nil, nil)
	if err != nil {
		return invalidAgentDefinitionError(svc, err)
	}
	if err := graph.validate(); err != nil {
		return invalidAgentDefinitionError(svc, err)
	}
	return nil
}

func promptAgentValidationDir(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (string, error) {
	if ref := strings.TrimSpace(
		svc.GetAdditionalProperties().GetFields()["$ref"].GetStringValue(),
	); ref != "" {
		definitionPath, err := resolveDeclaredRefPath(projectRoot, ref, svc.GetName())
		if err != nil {
			return "", err
		}
		return filepath.Dir(definitionPath), nil
	}
	return paths.JoinAllowRoot(projectRoot, svc.GetRelativePath())
}

func validateVoiceAgentServiceDefinition(
	svc *azdext.ServiceConfig,
	resolved *structpb.Struct,
	projectRoot string,
) error {
	voiceAgent, found, err := VoiceAgentFromResolvedService(svc, projectRoot)
	if err != nil {
		return err
	}
	if !found {
		return unsupportedAgentKindError(svc, agent_yaml.AgentKind(structKind(resolved)))
	}
	if err := agent_yaml.ValidateVoiceDefinitionOverlay(voiceAgent); err != nil {
		return invalidAgentDefinitionError(svc, err)
	}
	if err := validateAgentActivitySettings(
		svc,
		resolved,
		agent_yaml.ContainerAgent{},
	); err != nil {
		return err
	}
	return nil
}

func validateAgentActivitySettings(
	svc *azdext.ServiceConfig,
	resolved *structpb.Struct,
	hosted agent_yaml.ContainerAgent,
) error {
	effectiveService := proto.Clone(svc).(*azdext.ServiceConfig)
	effectiveService.AdditionalProperties = resolved
	settings, err := LoadServiceTargetAgentConfig(effectiveService)
	if err != nil {
		return invalidAgentDefinitionError(svc, err)
	}
	if _, err := ResolveActivityProfileForDeploy(hosted, settings.Activity); err != nil {
		return invalidAgentDefinitionError(svc, err)
	}
	return nil
}

func invalidAgentDefinitionError(svc *azdext.ServiceConfig, err error) error {
	return exterrors.ValidationFromError(
		err,
		exterrors.CodeInvalidAgentManifest,
		fmt.Sprintf("agent service %q is not valid", svc.GetName()),
		"fix the agent definition in azure.yaml or its explicitly referenced file",
	)
}

func unsupportedAgentKindError(
	svc *azdext.ServiceConfig,
	kind agent_yaml.AgentKind,
) error {
	return exterrors.Validation(
		exterrors.CodeUnsupportedAgentKind,
		fmt.Sprintf("agent service %q declares unsupported kind %q", svc.GetName(), kind),
		"set kind to one of: "+validAgentKindsText(),
	)
}

func validAgentKindsText() string {
	kinds := []agent_yaml.AgentKind{
		agent_yaml.AgentKindHosted,
		agent_yaml.AgentKindPrompt,
		agent_yaml.AgentKindPromptVoice,
		agent_yaml.AgentKindVoice,
	}
	values := make([]string, len(kinds))
	for i, kind := range kinds {
		values[i] = string(kind)
	}
	return strings.Join(values, ", ")
}
