// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/servicekey"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/types/known/structpb"
)

type agentDependencyPlan struct {
	Uses     []string
	Warnings []string
}

type agentDependencyReferences struct {
	connections []string
	toolboxes   []string
	skills      []string
	voiceAgent  string
}

func planAgentServiceUses(
	agentServiceName string,
	definition projectpkg.AgentDefinitionFile,
	currentUses []string,
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) (agentDependencyPlan, error) {
	agentServiceName = strings.TrimSpace(agentServiceName)
	if agentServiceName == "" {
		return agentDependencyPlan{}, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"an agent service name is required",
			"pass an azure.ai.agent service name from azure.yaml",
		)
	}
	if err := validateLoadedAgentDefinition(definition); err != nil {
		return agentDependencyPlan{}, err
	}

	agentService, exists := services[agentServiceName]
	if exists && agentService.GetHost() != AiAgentHost {
		return agentDependencyPlan{}, invalidServiceHostError(
			agentServiceName,
			agentService.GetHost(),
			AiAgentHost,
		)
	}

	projectServiceName, err := uniqueAgentProjectService(services)
	if err != nil {
		return agentDependencyPlan{}, err
	}

	newReferences, err := agentDependencyReferencesFromFile(
		definition,
		agentServiceName,
	)
	if err != nil {
		return agentDependencyPlan{}, err
	}
	newReferences, err = resolveNewAgentDependencies(
		agentServiceName,
		newReferences,
		services,
		projectRoot,
	)
	if err != nil {
		return agentDependencyPlan{}, err
	}

	if currentUses == nil && agentService != nil {
		currentUses = agentService.GetUses()
	}
	currentUses = slices.Clone(currentUses)

	oldOwnedUses, warnings := previousAgentOwnedUses(
		agentService,
		agentServiceName,
		services,
		projectRoot,
	)
	uses := mergeAgentServiceUses(
		projectServiceName,
		newReferences,
		currentUses,
		oldOwnedUses,
	)
	if err := validateAgentServiceUsesCycle(services, agentServiceName, uses); err != nil {
		return agentDependencyPlan{}, err
	}

	return agentDependencyPlan{Uses: uses, Warnings: warnings}, nil
}

func validateLoadedAgentDefinition(definition projectpkg.AgentDefinitionFile) error {
	kindValue, hasKind := definition.ResolvedProperties["kind"].(string)
	if len(definition.ResolvedProperties) == 0 ||
		!agent_yaml.IsValidAgentKind(definition.Kind) ||
		!hasKind ||
		agent_yaml.AgentKind(strings.TrimSpace(kindValue)) != definition.Kind {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"dependency planning requires a validated agent definition",
			"load the definition with project.LoadAgentDefinitionFile first",
		)
	}
	return nil
}

func uniqueAgentProjectService(
	services map[string]*azdext.ServiceConfig,
) (string, error) {
	var projectServices []string
	for _, serviceName := range slices.Sorted(maps.Keys(services)) {
		service := services[serviceName]
		if service != nil && service.GetHost() == AiProjectHost {
			projectServices = append(projectServices, serviceName)
		}
	}

	switch len(projectServices) {
	case 0:
		return "", exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			"the project has no azure.ai.project service",
			"add exactly one service with host: azure.ai.project to azure.yaml",
		)
	case 1:
		return projectServices[0], nil
	default:
		return "", exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("multiple azure.ai.project services are declared: %q", projectServices),
			"keep exactly one azure.ai.project service; multi-project routing is not supported",
		)
	}
}

func agentDependencyReferencesFromFile(
	definition projectpkg.AgentDefinitionFile,
	agentServiceName string,
) (agentDependencyReferences, error) {
	properties, err := structpb.NewStruct(definition.ResolvedProperties)
	if err != nil {
		return agentDependencyReferences{}, invalidAgentDependencyDefinition(err)
	}
	service := &azdext.ServiceConfig{
		Name:                 agentServiceName,
		Host:                 AiAgentHost,
		AdditionalProperties: properties,
	}
	serviceConfig, err := projectpkg.LoadServiceTargetAgentConfig(service)
	if err != nil {
		return agentDependencyReferences{}, invalidAgentDependencyDefinition(err)
	}

	references := agentDependencyReferences{}
	for _, toolbox := range serviceConfig.Toolboxes {
		name := strings.TrimSpace(toolbox.Name)
		if name == "" {
			return agentDependencyReferences{}, invalidAgentDependencyDefinition(
				fmt.Errorf("toolbox reference requires a non-empty name"),
			)
		}
		references.toolboxes = append(references.toolboxes, name)
	}

	switch definition.Kind {
	case agent_yaml.AgentKindPrompt:
		var prompt agent_yaml.PromptAgent
		if err := projectpkg.UnmarshalStruct(properties, &prompt); err != nil {
			return agentDependencyReferences{}, invalidAgentDependencyDefinition(err)
		}
		if err := prompt.ValidateAuthoredSkills(); err != nil {
			return agentDependencyReferences{}, invalidAgentDependencyDefinition(err)
		}
		references.connections = slices.Clone(prompt.Connections)
		for _, skill := range agent_yaml.PromptAgentSkillReferences(prompt) {
			if strings.TrimSpace(skill.Version) == "" {
				references.skills = append(
					references.skills,
					servicekey.SanitizeServiceName(skill.Name),
				)
			}
		}
	case agent_yaml.AgentKindPromptVoice, agent_yaml.AgentKindVoice:
		var voice agent_yaml.VoiceAgent
		if err := projectpkg.UnmarshalStruct(properties, &voice); err != nil {
			return agentDependencyReferences{}, invalidAgentDependencyDefinition(err)
		}
		if voice.ConversationEngine != nil &&
			strings.EqualFold(
				strings.TrimSpace(voice.ConversationEngine.Type),
				string(agent_yaml.VoiceModelTypeHostedAgent),
			) {
			references.voiceAgent = strings.TrimSpace(voice.ConversationEngine.Name)
		}
	}

	return references, nil
}

func invalidAgentDependencyDefinition(err error) error {
	return exterrors.ValidationFromError(
		err,
		exterrors.CodeInvalidAgentManifest,
		"agent definition contains invalid dependency references",
		"fix the direct agent definition and its local references",
	)
}

func resolveNewAgentDependencies(
	agentServiceName string,
	references agentDependencyReferences,
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) (agentDependencyReferences, error) {
	resolved := agentDependencyReferences{}
	for _, connectionRef := range references.connections {
		if strings.TrimSpace(connectionRef) == agentServiceName {
			return agentDependencyReferences{}, selfAgentDependencyError(
				agentServiceName,
				connectionRef,
				"Connection",
			)
		}
		identity, err := projectpkg.ResolveFoundryConnectionServiceIdentity(
			services,
			connectionRef,
			projectRoot,
		)
		if err != nil {
			return agentDependencyReferences{}, err
		}
		resolved.connections = append(resolved.connections, identity.ServiceKey)
	}

	for _, toolbox := range references.toolboxes {
		if err := requireAgentDependencyService(
			agentServiceName,
			toolbox,
			toolbox,
			"toolbox",
			AiToolboxHost,
			services,
		); err != nil {
			return agentDependencyReferences{}, err
		}
		resolved.toolboxes = append(resolved.toolboxes, toolbox)
	}

	for _, skill := range references.skills {
		if err := requireAgentDependencyService(
			agentServiceName,
			skill,
			skill,
			"skill",
			AiSkillHost,
			services,
		); err != nil {
			return agentDependencyReferences{}, err
		}
		resolved.skills = append(resolved.skills, skill)
	}

	if references.voiceAgent != "" {
		if err := requireAgentDependencyService(
			agentServiceName,
			references.voiceAgent,
			references.voiceAgent,
			"voice conversation engine",
			AiAgentHost,
			services,
		); err != nil {
			return agentDependencyReferences{}, err
		}
		_, isHosted, _, err := projectpkg.LoadHostedAgentDefinition(
			services[references.voiceAgent],
			projectRoot,
		)
		if err != nil {
			return agentDependencyReferences{}, err
		}
		if !isHosted {
			return agentDependencyReferences{}, exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf(
					"voice conversation engine %q does not reference a hosted agent service",
					references.voiceAgent,
				),
				"set conversationEngine.name to an azure.ai.agent service with kind: hosted",
			)
		}
		resolved.voiceAgent = references.voiceAgent
	}

	return resolved, nil
}

func requireAgentDependencyService(
	agentServiceName string,
	serviceName string,
	reference string,
	referenceType string,
	expectedHost string,
	services map[string]*azdext.ServiceConfig,
) error {
	if serviceName == agentServiceName {
		return selfAgentDependencyError(agentServiceName, reference, referenceType)
	}
	service, exists := services[serviceName]
	if !exists || service == nil {
		return exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf(
				"%s reference %q does not match a local service with host %q",
				referenceType,
				reference,
				expectedHost,
			),
			fmt.Sprintf(
				"add a service with key %q and host %q to azure.yaml",
				serviceName,
				expectedHost,
			),
		)
	}
	if service.GetHost() != expectedHost {
		return exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf(
				"%s reference %q resolves to service host %q instead of %q",
				referenceType,
				reference,
				service.GetHost(),
				expectedHost,
			),
			fmt.Sprintf(
				"use a local service with host %q for reference %q",
				expectedHost,
				reference,
			),
		)
	}
	return nil
}

func selfAgentDependencyError(agentServiceName, reference, referenceType string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf(
			"%s reference %q points to agent service %q itself",
			referenceType,
			reference,
			agentServiceName,
		),
		"remove the self-reference from the agent definition",
	)
}

func previousAgentOwnedUses(
	agentService *azdext.ServiceConfig,
	agentServiceName string,
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) (map[string]struct{}, []string) {
	owned := make(map[string]struct{})
	if agentService == nil {
		return owned, nil
	}

	properties, err := projectpkg.ResolveServiceConfigProps(agentService, projectRoot)
	if err != nil {
		return owned, []string{preservedAgentUsesWarning(err)}
	}
	if properties == nil || properties.GetFields()["kind"] == nil {
		return owned, nil
	}

	validation, err := projectpkg.ValidateAgentServiceDefinitionContent(
		agentService,
		projectRoot,
	)
	if err != nil {
		return owned, []string{preservedAgentUsesWarning(err)}
	}
	effectiveProperties := properties.AsMap()
	definition := projectpkg.AgentDefinitionFile{
		Properties:         effectiveProperties,
		ResolvedProperties: effectiveProperties,
		Kind:               validation.Kind,
		Name:               validation.Name,
	}
	references, err := agentDependencyReferencesFromFile(definition, agentServiceName)
	if err != nil {
		return owned, []string{preservedAgentUsesWarning(err)}
	}

	var warnings []string
	for _, connectionRef := range references.connections {
		identity, err := projectpkg.ResolveFoundryConnectionServiceIdentity(
			services,
			connectionRef,
			projectRoot,
		)
		if err != nil {
			warning := fmt.Sprintf(
				"could not resolve previous Connection reference %q; preserved its uses entry: %s",
				connectionRef,
				err,
			)
			if !slices.Contains(warnings, warning) {
				warnings = append(warnings, warning)
			}
			continue
		}
		owned[identity.ServiceKey] = struct{}{}
	}
	for _, toolbox := range references.toolboxes {
		owned[toolbox] = struct{}{}
	}
	for _, skill := range references.skills {
		owned[skill] = struct{}{}
	}
	if references.voiceAgent != "" {
		owned[references.voiceAgent] = struct{}{}
	}
	return owned, warnings
}

func preservedAgentUsesWarning(err error) string {
	return fmt.Sprintf(
		"could not safely parse the existing agent definition; preserved existing uses entries: %s",
		err,
	)
}

func mergeAgentServiceUses(
	projectServiceName string,
	references agentDependencyReferences,
	currentUses []string,
	oldOwnedUses map[string]struct{},
) []string {
	result := make([]string, 0, 1+len(references.connections)+
		len(references.toolboxes)+len(references.skills)+len(currentUses))
	seen := make(map[string]struct{}, cap(result))
	appendUnique := func(serviceName string) {
		if _, exists := seen[serviceName]; exists {
			return
		}
		seen[serviceName] = struct{}{}
		result = append(result, serviceName)
	}

	appendUnique(projectServiceName)
	for _, dependencies := range [][]string{
		references.connections,
		references.toolboxes,
		references.skills,
	} {
		for _, dependency := range dependencies {
			appendUnique(dependency)
		}
	}
	if references.voiceAgent != "" {
		appendUnique(references.voiceAgent)
	}
	for _, serviceName := range currentUses {
		if _, owned := oldOwnedUses[serviceName]; owned {
			continue
		}
		appendUnique(serviceName)
	}
	return result
}

func validateAgentServiceUsesCycle(
	services map[string]*azdext.ServiceConfig,
	agentServiceName string,
	uses []string,
) error {
	for _, dependencyName := range uses {
		if serviceDependsOn(services, dependencyName, agentServiceName, map[string]bool{}) {
			return exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf(
					"planned uses entry %q would create a dependency cycle for agent service %q",
					dependencyName,
					agentServiceName,
				),
				"remove the reverse dependency before adding this agent dependency",
			)
		}
	}
	return nil
}
