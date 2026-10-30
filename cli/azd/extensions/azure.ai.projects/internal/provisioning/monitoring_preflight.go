// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

func monitoringOutputKeys() []string {
	return []string{
		"AZURE_APPLICATION_INSIGHTS_RESOURCE_ID",
		"AZURE_APPLICATION_INSIGHTS_APP_ID",
		"AZURE_LOG_ANALYTICS_WORKSPACE_RESOURCE_ID",
		"AZURE_AI_PROJECT_APPINSIGHTS_CONNECTION_NAME",
		"AZD_FOUNDRY_MONITORING_MODE",
	}
}

func templateParameter(source *templateSource, name string) (any, bool) {
	if envelope, ok := source.parameters[name].(map[string]any); ok {
		value, present := envelope["value"]
		return value, present
	}
	parameters, _ := source.armTemplate["parameters"].(map[string]any)
	parameter, _ := parameters[name].(map[string]any)
	value, present := parameter["defaultValue"]
	return value, present
}

func monitoringParameter(source *templateSource, name string) (string, error) {
	value, present := templateParameter(source, name)
	if !present {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || strings.HasPrefix(text, "[") {
		return "", fmt.Errorf("monitoring parameter %s must resolve to a literal string before provisioning", name)
	}
	return strings.TrimSpace(text), nil
}

// Both Preview and Deploy resolve the same graph. This is read-only and never
// runs during State or agent deployment.
func (p *FoundryProvisioningProvider) prepareMonitoring(
	ctx context.Context,
	source *templateSource,
	client *armresources.DeploymentsClient,
	progress grpcbroker.ProgressFunc,
) error {
	mode, err := monitoringParameter(source, "monitoringMode")
	if err != nil {
		return err
	}
	if mode == "" || mode == "none" {
		return nil
	}
	if mode != "create" && mode != "reuse-connect" && mode != "already-connected" {
		return fmt.Errorf("monitoringMode must be none, create, reuse-connect, or already-connected")
	}
	componentID, err := monitoringParameter(source, "applicationInsightsResourceId")
	if err != nil {
		return err
	}
	if mode != "create" && componentID == "" {
		return fmt.Errorf("monitoring mode %s requires applicationInsightsResourceId", mode)
	}
	projectID, previous, err := p.resolveMonitoringTarget(ctx, source, client, progress)
	if err != nil {
		return err
	}
	project, err := synthesis.MonitoringResourceID(projectID, "Microsoft.CognitiveServices/accounts/projects")
	if err != nil || project.Parent == nil {
		return fmt.Errorf("monitoring target must identify a Foundry project")
	}
	resolver := synthesis.MonitoringResolver{Credential: p.credential, ClientOptions: p.armClientOptions}
	projectResource, projectExists, err := resolver.Resource(ctx, project, "2025-04-01-preview")
	if err != nil {
		return err
	}
	implicit := source.mode == templateModeEmbedded && p.synthResult != nil && !p.synthResult.MonitoringExplicit
	if implicit && projectExists && (previous == nil || previous.legacy) {
		// An upgrade, a changed identity input, or a stale cached ID is not
		// authorization to enable monitoring on a pre-existing project.
		source.parameters["monitoringMode"] = map[string]any{"value": "none"}
		return nil
	}
	account, accountExists, err := resolver.Resource(ctx, project.Parent, "2025-04-01-preview")
	if err != nil {
		return err
	}
	if projectExists && !accountExists {
		return fmt.Errorf("monitoring target account could not be verified")
	}
	connection, err := resolver.DiscoverConnection(ctx, project, projectExists, accountExists)
	if err != nil {
		return err
	}
	owned := previous != nil && previous.owned
	if connection != nil {
		if componentID != "" && !strings.EqualFold(componentID, connection.Target) {
			return fmt.Errorf("the selected component conflicts with the effective AppInsights connection")
		}
		if owned && mode == "create" {
			if err := p.validateMonitoringCreateGraph(ctx, source, previous, connection); err != nil {
				return err
			}
		} else if mode == "create" && source.mode != templateModeEmbedded {
			return fmt.Errorf("a compatible AppInsights connection already exists; preserve it with " +
				"monitoringMode already-connected and its applicationInsightsResourceId in the authored infrastructure")
		} else {
			mode = "already-connected"
			componentID = connection.Target
		}
		if err := resolver.ValidateComponent(ctx, connection.Target, mode == "create"); err != nil {
			return err
		}
		source.parameters["monitoringMode"] = map[string]any{"value": mode}
		source.parameters["applicationInsightsResourceId"] = map[string]any{"value": componentID}
		source.parameters["monitoringCreateConnection"] = map[string]any{"value": false}
		source.parameters["monitoringConnectionName"] = map[string]any{"value": connection.Name}
		return nil
	}
	if mode == "already-connected" {
		return fmt.Errorf("the declared existing AppInsights connection was not found; no connection was created")
	}
	if enabled, present := templateParameter(source, "enableNetworkIsolation"); present && enabled != false {
		return fmt.Errorf("automatic monitoring does not configure private-network ingestion; " +
			"preserve the existing connection or disable monitoring setup")
	}
	for _, resource := range []*armresources.GenericResource{account, projectResource} {
		if resource == nil {
			continue
		}
		properties, err := synthesis.MonitoringProperties(resource)
		if err != nil {
			return err
		}
		if err := synthesis.MonitoringNetworkPolicy(properties); err != nil {
			return err
		}
	}
	if mode == "reuse-connect" {
		if err := resolver.ValidateComponent(ctx, componentID, true); err != nil {
			return err
		}
	}
	if mode == "create" {
		if err := p.validateMonitoringCreateGraph(ctx, source, previous, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *FoundryProvisioningProvider) resolveMonitoringTarget(
	ctx context.Context,
	source *templateSource,
	client *armresources.DeploymentsClient,
	progress grpcbroker.ProgressFunc,
) (string, *monitoringTargetBinding, error) {
	bindingParameters := map[string]any{}
	for _, name := range []string{
		"location", "resourceGroupName", "resourceTokenSalt", "foundryProjectName", "projectResourceId",
	} {
		if value, present := templateParameter(source, name); present {
			bindingParameters[name] = map[string]any{"value": value}
		}
	}
	var previous *monitoringTargetBinding
	response, err := client.GetAtSubscriptionScope(ctx, p.deploymentName(), nil)
	if err != nil && !isNotFound(err) {
		return "", nil, synthesis.MonitoringReadError("read previous Foundry deployment", err)
	}
	if err == nil {
		previous = previousMonitoringTarget(bindingParameters, response.Properties, p.subID)
	}
	existingID := ""
	if _, present := templateParameter(source, "projectResourceId"); present {
		existingID, err = monitoringParameter(source, "projectResourceId")
		if err != nil || existingID == "" {
			return "", nil, fmt.Errorf("monitoring requires the actual existing projectResourceId")
		}
	}
	mode, err := monitoringParameter(source, "monitoringMode")
	if err != nil {
		return "", nil, err
	}
	if existingID != "" && mode != "create" {
		return existingID, previous, nil
	}
	poller, err := client.BeginWhatIfAtSubscriptionScope(ctx, p.deploymentName(), armresources.DeploymentWhatIf{
		Location: new(p.location),
		Properties: &armresources.DeploymentWhatIfProperties{
			Template: source.armTemplate, Parameters: source.parameters,
			Mode: new(armresources.DeploymentModeIncremental),
		},
	}, nil)
	if err != nil {
		return "", nil, synthesis.MonitoringReadError("resolve monitoring target with ARM What-If", err)
	}
	result, err := pollWithProgress(ctx, poller, progress, "Resolving Foundry monitoring target")
	if err != nil {
		return "", nil, synthesis.MonitoringReadError("resolve monitoring target with ARM What-If", err)
	}
	if err := whatIfFailure(result.WhatIfOperationResult); err != nil {
		return "", nil, err
	}
	id := existingID
	if id == "" {
		group, err := monitoringParameter(source, "resourceGroupName")
		if err != nil {
			return "", nil, err
		}
		name, err := monitoringParameter(source, "foundryProjectName")
		if err != nil {
			return "", nil, err
		}
		id, err = monitoringProjectFromWhatIf(result.WhatIfOperationResult, p.subID, group, name)
		if err != nil {
			return "", nil, err
		}
	}
	if err := validateMonitoringConnectionPlan(&result.WhatIfOperationResult, id); err != nil {
		return "", nil, err
	}
	if previous != nil && !strings.EqualFold(previous.projectID, id) {
		previous = nil
	}
	if previous == nil {
		previous = &monitoringTargetBinding{projectID: id, legacy: true}
	}
	previous.plan = &result.WhatIfOperationResult
	if !previous.owned && mode == "create" && monitoringFailedCreateAttempt(bindingParameters, response.Properties) {
		exported, err := client.ExportTemplateAtSubscriptionScope(ctx, p.deploymentName(), nil)
		if err != nil {
			return "", nil, synthesis.MonitoringReadError("verify the previous failed monitoring graph", err)
		}
		before, beforeErr := json.Marshal(exported.Template)
		after, afterErr := json.Marshal(source.armTemplate)
		if beforeErr != nil || afterErr != nil || !bytes.Equal(before, after) {
			return "", nil, fmt.Errorf("the failed deployment used a different monitoring graph; " +
				"restore its infrastructure or explicitly configure existing-component reuse before retrying")
		}
		previous.owned, previous.legacy, previous.retry = true, false, true
	}
	return id, previous, nil
}

// A failed deployment's outputs are not an identity binding. Its exact graph
// and identity inputs can still authorize retry after What-If resolves targets.
func monitoringFailedCreateAttempt(
	parameters map[string]any, previous *armresources.DeploymentPropertiesExtended,
) bool {
	if previous == nil || previous.ProvisioningState == nil ||
		(*previous.ProvisioningState != armresources.ProvisioningStateFailed &&
			*previous.ProvisioningState != armresources.ProvisioningStateCanceled) ||
		!monitoringIdentityMatches(parameters, previous.Parameters) {
		return false
	}
	old, _ := previous.Parameters.(map[string]any)
	if mode, _ := armStringParameter(old, "monitoringMode"); mode != "create" {
		return false
	}
	return true
}
