// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"strings"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func monitoringPlannedResource(
	result *armresources.WhatIfOperationResult, resourceType, subscriptionID, resourceGroup string,
) (string, error) {
	if result == nil || result.Properties == nil {
		return "", fmt.Errorf("ARM What-If did not resolve monitoring resources; no monitoring writes were authorized")
	}
	selected := ""
	for _, change := range result.Properties.Changes {
		if change == nil || change.ResourceID == nil || change.ChangeType == nil ||
			*change.ChangeType == armresources.ChangeTypeIgnore {
			continue
		}
		id, err := arm.ParseResourceID(*change.ResourceID)
		if err != nil || !strings.EqualFold(id.ResourceType.String(), resourceType) {
			continue
		}
		if !strings.EqualFold(id.SubscriptionID, subscriptionID) || !strings.EqualFold(id.ResourceGroupName, resourceGroup) {
			return "", fmt.Errorf("the monitoring creation graph must stay in the selected owned resource group")
		}
		switch *change.ChangeType {
		case armresources.ChangeTypeCreate, armresources.ChangeTypeModify,
			armresources.ChangeTypeNoChange, armresources.ChangeTypeDeploy:
		default:
			return "", fmt.Errorf("ARM What-If could not safely resolve the monitoring creation graph")
		}
		if selected != "" && !strings.EqualFold(selected, id.String()) {
			return "", fmt.Errorf("ARM What-If resolved multiple monitoring resources of type %s", resourceType)
		}
		if after, ok := change.After.(map[string]any); ok {
			if properties, ok := after["properties"].(map[string]any); ok {
				if err := monitoringKeyPolicy(properties); err != nil {
					return "", err
				}
			}
		}
		selected = id.String()
	}
	if selected == "" {
		return "", fmt.Errorf("ARM What-If did not resolve the declared %s; no monitoring writes were authorized", resourceType)
	}
	return selected, nil
}

func validateMonitoringConnectionPlan(result *armresources.WhatIfOperationResult, projectID string) error {
	if result == nil || result.Properties == nil {
		return nil
	}
	project, err := monitoringResourceID(projectID, "Microsoft.CognitiveServices/accounts/projects")
	if err != nil {
		return err
	}
	expected := projectID + "/connections/" + synthesis.MonitoringConnectionName(project.Name)
	for _, change := range result.Properties.Changes {
		if change == nil || change.ResourceID == nil || change.ChangeType == nil ||
			*change.ChangeType == armresources.ChangeTypeIgnore {
			continue
		}
		id, err := arm.ParseResourceID(*change.ResourceID)
		if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects/connections") ||
			!strings.HasPrefix(strings.ToLower(id.Name), "azd-appinsights-") {
			continue
		}
		if !strings.EqualFold(id.String(), expected) {
			return fmt.Errorf("the monitoring graph targets a different project connection; no monitoring writes were authorized")
		}
	}
	return nil
}

// Check the ARM-resolved names before allowing ordinary resource PUTs. A stable
// generated name alone never authorizes adopting a pre-existing component.
func (p *FoundryProvisioningProvider) validateMonitoringCreateGraph(
	ctx context.Context, source *templateSource, binding *monitoringTargetBinding, connection *monitoringConnection,
) error {
	group, err := monitoringParameter(source, "resourceGroupName")
	if err != nil {
		return err
	}
	outputs := armOutputsToProto(binding.outputs)
	for _, resource := range []struct {
		resourceType string
		output       string
		version      string
	}{
		{"Microsoft.Insights/components", "AZURE_APPLICATION_INSIGHTS_RESOURCE_ID", "2020-02-02"},
		{"Microsoft.OperationalInsights/workspaces", "AZURE_LOG_ANALYTICS_WORKSPACE_RESOURCE_ID", "2023-09-01"},
	} {
		id, err := monitoringPlannedResource(binding.plan, resource.resourceType, p.subID, group)
		if err != nil {
			return err
		}
		if connection != nil && resource.resourceType == "Microsoft.Insights/components" &&
			!strings.EqualFold(connection.target, id) {
			return fmt.Errorf("the authored creation graph would replace the effective monitoring destination")
		}
		parsed, err := monitoringResourceID(id, resource.resourceType)
		if err != nil {
			return err
		}
		existing, found, err := p.monitoringResource(ctx, parsed, resource.version)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		expected := outputs[resource.output]
		if !binding.owned || (!binding.retry && (expected == nil || !strings.EqualFold(expected.Value, id))) {
			return fmt.Errorf("a monitoring resource already occupies the planned name and is not owned by this deployment; " +
				"use applicationInsightsResourceId to reuse it without adopting or changing it")
		}
		properties, err := monitoringProperties(existing)
		if err != nil {
			return err
		}
		if err := monitoringKeyPolicy(properties); err != nil {
			return err
		}
	}
	return nil
}
