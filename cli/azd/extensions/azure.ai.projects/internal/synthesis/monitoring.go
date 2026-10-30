// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"go.yaml.in/yaml/v3"
)

func monitoringParameters(node yaml.Node, in Input, newProject bool) (map[string]any, error) {
	if in.InfrastructureOwnsMonitoring {
		return map[string]any{}, nil
	}
	enabled := newProject
	id := ""
	hasID := false
	if node.Kind != 0 {
		if node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("monitoring must be an object")
		}
		var fields map[string]yaml.Node
		if err := node.Decode(&fields); err != nil {
			return nil, fmt.Errorf("read monitoring configuration: %w", err)
		}
		enabled = true
		for name, value := range fields {
			switch name {
			case "enabled":
				if value.Tag != "!!bool" {
					return nil, fmt.Errorf("monitoring.enabled must be a boolean")
				}
				if err := value.Decode(&enabled); err != nil {
					return nil, fmt.Errorf("read monitoring.enabled: %w", err)
				}
			case "applicationInsightsResourceId":
				hasID = true
				if value.Tag != "!!str" {
					return nil, fmt.Errorf("monitoring.applicationInsightsResourceId must be a string")
				}
				id = strings.TrimSpace(value.Value)
			default:
				return nil, fmt.Errorf("unknown monitoring setting %q", name)
			}
		}
	}
	params := map[string]any{"monitoringMode": "none", "applicationInsightsResourceId": ""}
	if !enabled {
		return params, nil
	}
	params["monitoringMode"] = "create"
	if id == "" {
		if hasID {
			return nil, fmt.Errorf("monitoring.applicationInsightsResourceId must be a nonempty component ID")
		}
		return params, nil
	}
	if err := ValidateEnvReferences(id); err != nil {
		return nil, fmt.Errorf("monitoring.applicationInsightsResourceId: %w", err)
	}
	if !in.PreserveVarRefs || !containsVarRef(id) {
		var err error
		id, err = resolveVars(id, in.Env)
		if err != nil {
			return nil, fmt.Errorf("resolve monitoring.applicationInsightsResourceId: %w", err)
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("monitoring.applicationInsightsResourceId must resolve to a nonempty component ID")
		}
		resource, err := arm.ParseResourceID(id)
		if err != nil || resource.SubscriptionID == "" || resource.ResourceGroupName == "" ||
			!strings.EqualFold(resource.ResourceType.String(), "Microsoft.Insights/components") {
			return nil, fmt.Errorf("monitoring.applicationInsightsResourceId must identify an Application Insights component")
		}
	}
	params["monitoringMode"] = "reuse-connect"
	params["applicationInsightsResourceId"] = id
	return params, nil
}
