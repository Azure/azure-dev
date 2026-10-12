// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/projectconfig"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

	definition, err := resolveServiceProperties(svc, projectRoot)
	if err != nil {
		return nil, err
	}

	environment := make(map[string]string)
	if guidanceDefinitionHasKind(definition) {
		values, err := decodeDefinitionEnvironment(definition)
		if err != nil {
			return nil, fmt.Errorf("decode agent environment: %w", err)
		}
		maps.Copy(environment, values)
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
