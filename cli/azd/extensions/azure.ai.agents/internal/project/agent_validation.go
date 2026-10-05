// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
		if err := validateHostedAgentServiceDefinition(svc, resolved, projectRoot); err != nil {
			return AgentDefinitionValidation{}, err
		}
	case agent_yaml.AgentKindPrompt:
		if err := validatePromptAgentServiceDefinition(svc, projectRoot); err != nil {
			return AgentDefinitionValidation{}, err
		}
		if result.Name == "" {
			result.Name = svc.GetName()
		}
	case agent_yaml.AgentKindPromptVoice, agent_yaml.AgentKindVoice:
		if err := validateVoiceAgentServiceDefinition(svc, projectRoot); err != nil {
			return AgentDefinitionValidation{}, err
		}
	case agent_yaml.AgentKindWorkflow:
		if err := validateDirectAgentDefinition(resolved.AsMap()); err != nil {
			return AgentDefinitionValidation{}, err
		}
	default:
		return AgentDefinitionValidation{}, unsupportedAgentKindError(svc, kind)
	}

	return result, nil
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

	effectiveService := *svc
	effectiveService.AdditionalProperties = resolved
	settings, err := LoadServiceTargetAgentConfig(&effectiveService)
	if err != nil {
		return invalidAgentDefinitionError(svc, err)
	}
	if _, err := ResolveActivityProfileForDeploy(hosted, settings.Activity); err != nil {
		return invalidAgentDefinitionError(svc, err)
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
	projectRoot string,
) error {
	_, found, err := VoiceAgentFromResolvedService(svc, projectRoot)
	if err != nil {
		return err
	}
	if !found {
		return unsupportedAgentKindError(svc, agent_yaml.AgentKind(structKind(ServiceConfigProps(svc))))
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
		agent_yaml.AgentKindWorkflow,
	}
	values := make([]string, len(kinds))
	for i, kind := range kinds {
		values[i] = string(kind)
	}
	return strings.Join(values, ", ")
}
