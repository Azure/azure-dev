// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"
	"slices"

	agentSchemas "azureaiagent/schemas"
)

// AgentDefinitionOwnedServiceProperties lists Agent service fields.
// Core-owned service fields are excluded.
func AgentDefinitionOwnedServiceProperties() ([]string, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(agentSchemas.AgentDefinitionSchema(), &schema); err != nil {
		return nil, fmt.Errorf("decode embedded agent schema properties: %w", err)
	}
	if schema.Properties == nil {
		return nil, fmt.Errorf("embedded agent schema has no top-level properties")
	}

	properties := make([]string, 0, len(schema.Properties)+1)
	for name := range schema.Properties {
		if slices.Contains(coreServiceKeys, name) {
			continue
		}
		properties = append(properties, name)
	}

	properties = append(properties, "environmentVariables")
	slices.Sort(properties)
	return slices.Compact(properties), nil
}
