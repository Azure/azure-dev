// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type initAgentDefinitionProbe struct {
	definition agent_yaml.ContainerAgent
	kind       agent_yaml.AgentKind
	isHosted   bool
	found      bool
}

// probeAgentDefinitionForInit resolves the same unified direct/root-$ref
// service definitions accepted by runtime commands.
func probeAgentDefinitionForInit(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (initAgentDefinitionProbe, error) {
	if svc == nil {
		return initAgentDefinitionProbe{}, nil
	}
	if svc.GetHost() == AiAgentHost && svc.GetConfig() != nil && len(svc.GetConfig().GetFields()) > 0 {
		return initAgentDefinitionProbe{}, exterrors.Validation(
			exterrors.CodeDeprecatedAgentServiceConfig,
			fmt.Sprintf("service %q uses the unsupported nested config block", svc.GetName()),
			"move the agent definition to service-level properties in azure.yaml, "+
				"or add a service-level $ref to a direct agent definition",
		)
	}

	props := svc.GetAdditionalProperties()
	kind, err := initAgentKindFromProperties(props, projectRoot)
	if err != nil {
		return initAgentDefinitionProbe{}, err
	}

	candidate := proto.Clone(svc).(*azdext.ServiceConfig)
	if props != nil {
		candidate.AdditionalProperties = proto.Clone(props).(*structpb.Struct)
	}
	candidate.Config = nil
	definition, isHosted, found, _, err := projectpkg.AgentDefinitionFromResolvedServiceForInit(
		candidate,
		projectRoot,
	)
	return initAgentDefinitionProbe{
		definition: definition,
		kind:       agent_yaml.AgentKind(kind),
		isHosted:   isHosted,
		found:      found,
	}, err
}

func probeAgentKindForInit(svc *azdext.ServiceConfig, projectRoot string) (agent_yaml.AgentKind, error) {
	probe, err := probeAgentDefinitionForInit(svc, projectRoot)
	if err != nil {
		return "", err
	}
	return probe.kind, nil
}

func initAgentKindFromProperties(props *structpb.Struct, projectRoot string) (string, error) {
	if props == nil || len(props.GetFields()) == 0 {
		return "", nil
	}

	values := props.AsMap()
	if kind, _ := values["kind"].(string); strings.TrimSpace(kind) != "" {
		return strings.TrimSpace(kind), nil
	}
	if _, hasRef := values["$ref"]; !hasRef {
		return "", nil
	}

	resolved, err := foundry.ResolveFileRefs(values, projectRoot)
	if err != nil {
		return "", err
	}
	kind, _ := resolved["kind"].(string)
	return strings.TrimSpace(kind), nil
}
