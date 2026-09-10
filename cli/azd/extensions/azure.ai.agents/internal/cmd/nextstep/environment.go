// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/pkg/projectconfig"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

type guidanceAgentDefinition struct {
	Kind                 string                           `json:"kind" yaml:"kind"`
	EnvironmentVariables []agent_yaml.EnvironmentVariable `json:"environmentVariables" yaml:"environmentVariables"`
}

// loadEffectiveAgentEnvironment returns environment templates for an
// agent service. Service-level env values take precedence.
func loadEffectiveAgentEnvironment(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (map[string]string, error) {
	if svc == nil {
		return nil, fmt.Errorf("service configuration is nil")
	}

	var definition map[string]any
	for _, candidate := range []struct {
		name  string
		props *structpb.Struct
	}{
		{name: "service-level properties", props: svc.GetAdditionalProperties()},
		{name: "deprecated config", props: svc.GetConfig()},
	} {
		resolved, found, err := resolveGuidanceDefinition(
			candidate.name,
			candidate.props,
			projectRoot,
		)
		if err != nil {
			return nil, err
		}
		if found {
			definition = resolved
			break
		}
	}

	environment := make(map[string]string)
	if definition != nil {
		values, err := decodeDefinitionEnvironment(definition)
		if err != nil {
			return nil, fmt.Errorf("decode agent environment: %w", err)
		}
		maps.Copy(environment, values)
	} else {
		values, found, err := loadLegacyAgentEnvironment(svc, projectRoot)
		if err != nil {
			return nil, err
		}
		if found {
			maps.Copy(environment, values)
		}
	}

	serviceEnvironment, err := projectconfig.LoadServiceEnvironment(
		projectRoot,
		svc.GetName(),
	)
	if err != nil {
		return nil, fmt.Errorf("load service-level env: %w", err)
	}
	maps.Copy(environment, serviceEnvironment)

	return environment, nil
}

func resolveGuidanceDefinition(
	name string,
	props *structpb.Struct,
	projectRoot string,
) (map[string]any, bool, error) {
	if props == nil || len(props.GetFields()) == 0 {
		return nil, false, nil
	}

	resolved, err := foundry.ResolveFileRefs(props.AsMap(), projectRoot)
	if err != nil {
		return nil, false, fmt.Errorf("resolve %s: %w", name, err)
	}
	return resolved, guidanceDefinitionHasKind(resolved), nil
}

func guidanceDefinitionHasKind(values map[string]any) bool {
	kind, ok := values["kind"].(string)
	return ok && strings.TrimSpace(kind) != ""
}

func decodeDefinitionEnvironment(
	values map[string]any,
) (map[string]string, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}

	var definition guidanceAgentDefinition
	if err := json.Unmarshal(data, &definition); err != nil {
		return nil, err
	}

	environment := environmentVariablesToMap(definition.EnvironmentVariables)
	return environment, nil
}

func loadLegacyAgentEnvironment(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (map[string]string, bool, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return nil, false, nil
	}

	for _, name := range []string{"agent.yaml", "agent.yml"} {
		definitionPath, err := paths.JoinAllowRoot(
			projectRoot,
			svc.GetRelativePath(),
			name,
		)
		if err != nil {
			return nil, false, fmt.Errorf(
				"resolve legacy agent file path: %w",
				err,
			)
		}

		data, err := os.ReadFile(definitionPath) //nolint:gosec
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, false, fmt.Errorf(
				"read legacy agent file %q: %w",
				definitionPath,
				err,
			)
		}

		var definition agent_yaml.ContainerAgent
		if err := yaml.Unmarshal(data, &definition); err != nil {
			return nil, false, fmt.Errorf(
				"parse legacy agent file %q: %w",
				definitionPath,
				err,
			)
		}
		if definition.EnvironmentVariables == nil {
			return map[string]string{}, true, nil
		}
		return environmentVariablesToMap(*definition.EnvironmentVariables), true, nil
	}

	return nil, false, nil
}

func environmentVariablesToMap(
	values []agent_yaml.EnvironmentVariable,
) map[string]string {
	environment := make(map[string]string, len(values))
	for _, value := range values {
		environment[value.Name] = value.Value
	}
	return environment
}

func sortedEnvironmentValues(environment map[string]string) []string {
	if len(environment) == 0 {
		return nil
	}

	keys := slices.Sorted(maps.Keys(environment))
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, environment[key])
	}
	return values
}
