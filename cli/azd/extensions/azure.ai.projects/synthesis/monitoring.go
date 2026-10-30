// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"go.yaml.in/yaml/v3"
)

// monitoringSettings is the parsed, unvalidated monitoring block of an
// azure.ai.project service.
type monitoringSettings struct {
	declared bool
	enabled  bool
	id       string
	hasID    bool
}

// newProjectMonitoringParameters enables monitoring unless the block disables it.
func newProjectMonitoringParameters(node yaml.Node, in Input) (map[string]any, error) {
	if in.InfrastructureOwnsMonitoring {
		return map[string]any{}, nil
	}
	settings, err := parseMonitoringSettings(node)
	if err != nil {
		return nil, err
	}
	return buildMonitoringParameters(settings, !settings.declared || settings.enabled, in)
}

// existingProjectMonitoringParameters enables monitoring only when the block
// is declared and does not disable it. Creating a new component for an
// existing project is not supported, so an enabled block must name one.
func existingProjectMonitoringParameters(node yaml.Node, in Input) (map[string]any, error) {
	if in.InfrastructureOwnsMonitoring {
		return map[string]any{}, nil
	}
	settings, err := parseMonitoringSettings(node)
	if err != nil {
		return nil, err
	}
	params, err := buildMonitoringParameters(settings, settings.declared && settings.enabled, in)
	if err != nil {
		return nil, err
	}
	if params["monitoringMode"] == "create" {
		return nil, fmt.Errorf("monitoring for an existing Foundry project requires " +
			"monitoring.applicationInsightsResourceId; creating Application Insights for an existing project is not supported")
	}
	return params, nil
}

func buildMonitoringParameters(settings monitoringSettings, enabled bool, in Input) (map[string]any, error) {
	params := map[string]any{"monitoringMode": "none", "applicationInsightsResourceId": ""}
	if !enabled {
		return params, nil
	}
	params["monitoringMode"] = "create"
	if settings.id == "" {
		if settings.hasID {
			return nil, fmt.Errorf("monitoring.applicationInsightsResourceId must be a nonempty component ID")
		}
		return params, nil
	}
	id, err := resolveMonitoringResourceID(settings.id, in)
	if err != nil {
		return nil, err
	}
	params["monitoringMode"] = "reuse-connect"
	params["applicationInsightsResourceId"] = id
	return params, nil
}

func parseMonitoringSettings(node yaml.Node) (monitoringSettings, error) {
	var settings monitoringSettings
	if node.Kind == 0 {
		return settings, nil
	}
	if node.Kind != yaml.MappingNode {
		return settings, fmt.Errorf("monitoring must be an object")
	}
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return settings, fmt.Errorf("read monitoring configuration: %w", err)
	}
	settings.declared = true
	settings.enabled = true
	for name, value := range fields {
		switch name {
		case "enabled":
			if value.Tag != "!!bool" {
				return settings, fmt.Errorf("monitoring.enabled must be a boolean")
			}
			if err := value.Decode(&settings.enabled); err != nil {
				return settings, fmt.Errorf("read monitoring.enabled: %w", err)
			}
		case "applicationInsightsResourceId":
			settings.hasID = true
			if value.Tag != "!!str" {
				return settings, fmt.Errorf("monitoring.applicationInsightsResourceId must be a string")
			}
			settings.id = strings.TrimSpace(value.Value)
		default:
			return settings, fmt.Errorf("unknown monitoring setting %q", name)
		}
	}
	return settings, nil
}

// resolveMonitoringResourceID validates variable references and, unless the
// reference is deliberately preserved, resolves and validates the component ID.
func resolveMonitoringResourceID(id string, in Input) (string, error) {
	if err := ValidateEnvReferences(id); err != nil {
		return "", fmt.Errorf("monitoring.applicationInsightsResourceId: %w", err)
	}
	if in.PreserveVarRefs && containsVarRef(id) {
		return id, nil
	}
	id, err := resolveVars(id, in.Env)
	if err != nil {
		return "", fmt.Errorf("resolve monitoring.applicationInsightsResourceId: %w", err)
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("monitoring.applicationInsightsResourceId must resolve to a nonempty component ID")
	}
	resource, err := arm.ParseResourceID(id)
	if err != nil || resource.SubscriptionID == "" || resource.ResourceGroupName == "" ||
		!strings.EqualFold(resource.ResourceType.String(), "Microsoft.Insights/components") {
		return "", fmt.Errorf("monitoring.applicationInsightsResourceId must identify an Application Insights component")
	}
	return id, nil
}
