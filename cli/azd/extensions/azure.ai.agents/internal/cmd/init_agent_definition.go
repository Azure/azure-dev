// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type initAgentDefinitionProbe struct {
	definition agent_yaml.ContainerAgent
	kind       agent_yaml.AgentKind
	isHosted   bool
	found      bool
}

// probeAgentDefinitionForInit preserves the legacy sources that init must
// recognize while runtime commands require a unified service definition.
func probeAgentDefinitionForInit(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (initAgentDefinitionProbe, error) {
	if svc == nil {
		return initAgentDefinitionProbe{}, nil
	}

	for _, props := range []*structpb.Struct{
		svc.GetAdditionalProperties(),
		svc.GetConfig(),
	} {
		kind, err := initAgentKindFromProperties(props, projectRoot)
		if err != nil {
			return initAgentDefinitionProbe{}, err
		}
		if kind == "" {
			continue
		}

		// Present each candidate as the supported service-level shape. This
		// keeps parsing, validation, and $ref behavior on the shared loader
		// without making runtime commands accept config-nested definitions.
		candidate := proto.Clone(svc).(*azdext.ServiceConfig)
		candidate.AdditionalProperties = proto.Clone(props).(*structpb.Struct)
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

	for _, name := range []string{"agent.yaml", "agent.yml"} {
		path, err := paths.JoinAllowRoot(projectRoot, svc.GetRelativePath(), name)
		if err != nil {
			return initAgentDefinitionProbe{}, fmt.Errorf(
				"invalid service path for %s: %w",
				svc.GetName(),
				err,
			)
		}
		//nolint:gosec // path is constrained to the project root above
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return initAgentDefinitionProbe{}, fmt.Errorf("read %s: %w", path, err)
		}

		var header struct {
			Kind agent_yaml.AgentKind `yaml:"kind"`
		}
		if err := yaml.Unmarshal(data, &header); err != nil {
			return initAgentDefinitionProbe{}, fmt.Errorf("parse %s: %w", path, err)
		}
		if err := agent_yaml.ValidateAgentDefinition(data); err != nil {
			return initAgentDefinitionProbe{}, err
		}

		probe := initAgentDefinitionProbe{
			kind:     header.Kind,
			isHosted: header.Kind == agent_yaml.AgentKindHosted,
			found:    true,
		}
		if !probe.isHosted {
			return probe, nil
		}

		definition, err := loadAgentDefinitionFile(path)
		if err != nil {
			return initAgentDefinitionProbe{}, err
		}
		probe.definition = *definition
		return probe, nil
	}

	return initAgentDefinitionProbe{}, nil
}

func probeAgentKindForInit(svc *azdext.ServiceConfig, projectRoot string) (agent_yaml.AgentKind, error) {
	if svc == nil {
		return "", nil
	}

	for _, props := range []*structpb.Struct{
		svc.GetAdditionalProperties(),
		svc.GetConfig(),
	} {
		kind, err := initAgentKindFromProperties(props, projectRoot)
		if err != nil {
			return "", err
		}
		if kind != "" {
			return agent_yaml.AgentKind(kind), nil
		}
	}

	for _, name := range []string{"agent.yaml", "agent.yml"} {
		path, err := paths.JoinAllowRoot(projectRoot, svc.GetRelativePath(), name)
		if err != nil {
			continue
		}
		//nolint:gosec // path is constrained to the project root above
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		var header struct {
			Kind agent_yaml.AgentKind `yaml:"kind"`
		}
		if err := yaml.Unmarshal(data, &header); err != nil {
			return "", fmt.Errorf("parse %s: %w", path, err)
		}
		return header.Kind, nil
	}

	return "", nil
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
