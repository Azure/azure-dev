// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"strings"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type monitoringCleanup struct {
	connectionName string
	ownsResources  bool
}

func monitoringOutputKeys() []string {
	return []string{
		"AZURE_APPLICATION_INSIGHTS_RESOURCE_ID",
		"AZURE_APPLICATION_INSIGHTS_APP_ID",
		"AZURE_LOG_ANALYTICS_WORKSPACE_RESOURCE_ID",
		"AZURE_AI_PROJECT_APPINSIGHTS_CONNECTION_NAME",
		"AZD_FOUNDRY_MONITORING_MODE",
	}
}

func (p *FoundryProvisioningProvider) monitoringResourceInOwnedGroup(id *arm.ResourceID) bool {
	return strings.EqualFold(id.SubscriptionID, p.subID) && strings.EqualFold(id.ResourceGroupName, p.rgName)
}

func (p *FoundryProvisioningProvider) monitoringOwnershipMatches(tags map[string]*string) bool {
	return tags["azd-created-by"] != nil && *tags["azd-created-by"] == "azure.ai.projects" &&
		tags["azd-environment"] != nil && *tags["azd-environment"] == p.envName &&
		tags["azd-monitoring-project"] != nil &&
		strings.EqualFold(*tags["azd-monitoring-project"], p.existingProjectID)
}

// Inspect live resources rather than restoring hidden monitoring state after
// opt-out. The existing adjunct-group binding remains the deletion boundary.
func (p *FoundryProvisioningProvider) prepareMonitoringCleanup(ctx context.Context) (monitoringCleanup, error) {
	project, err := monitoringResourceID(p.existingProjectID, "Microsoft.CognitiveServices/accounts/projects")
	if err != nil || project.Parent == nil {
		return monitoringCleanup{}, fmt.Errorf("monitoring cleanup requires the bound existing Foundry project")
	}
	client, err := armresources.NewClient(p.subID, p.credential, p.armClientOptions)
	if err != nil {
		return monitoringCleanup{}, err
	}
	if p.existingAcrMode != "create" {
		registries := client.NewListByResourceGroupPager(p.rgName, &armresources.ClientListByResourceGroupOptions{
			Filter: new("resourceType eq 'Microsoft.ContainerRegistry/registries'"),
		})
		for registries.More() {
			page, err := registries.NextPage(ctx)
			if isNotFound(err) {
				break
			}
			if err != nil {
				return monitoringCleanup{}, monitoringReadError("inspect retained registry ownership before cleanup", err)
			}
			if len(page.Value) != 0 {
				return monitoringCleanup{}, fmt.Errorf("the owned group still contains a registry without its create-mode binding; " +
					"restore the registry configuration before cleanup to avoid leaving a dangling project connection")
			}
		}
	}
	pager := client.NewListByResourceGroupPager(p.rgName, &armresources.ClientListByResourceGroupOptions{
		Filter: new("resourceType eq 'Microsoft.Insights/components'"),
	})
	componentID := ""
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if isNotFound(err) {
			break
		}
		if err != nil {
			return monitoringCleanup{}, monitoringReadError("inspect owned monitoring resources before cleanup", err)
		}
		for _, resource := range page.Value {
			if resource == nil || resource.ID == nil {
				return monitoringCleanup{}, fmt.Errorf("monitoring cleanup resource discovery is incomplete")
			}
			id, err := monitoringResourceID(*resource.ID, "Microsoft.Insights/components")
			if err != nil || !p.monitoringResourceInOwnedGroup(id) {
				return monitoringCleanup{}, fmt.Errorf("monitoring cleanup discovered an unexpected resource scope")
			}
			if creator := resource.Tags["azd-created-by"]; creator == nil || *creator != "azure.ai.projects" {
				continue
			}
			if !p.monitoringOwnershipMatches(resource.Tags) {
				return monitoringCleanup{}, fmt.Errorf("the owned group contains monitoring for another project or environment; " +
					"refusing ambiguous cleanup")
			}
			if componentID != "" && !strings.EqualFold(componentID, id.String()) {
				return monitoringCleanup{}, fmt.Errorf("the owned group contains multiple monitoring destinations; " +
					"refusing ambiguous cleanup")
			}
			componentID = id.String()
		}
	}

	connections, err := armcognitiveservices.NewProjectConnectionsClient(
		project.SubscriptionID, p.credential, p.armClientOptions)
	if err != nil {
		return monitoringCleanup{}, err
	}
	name := synthesis.MonitoringConnectionName(project.Name)
	connection, err := connections.Get(
		ctx, project.ResourceGroupName, project.Parent.Name, project.Name, name, nil)
	if isNotFound(err) {
		_, legacyErr := connections.Get(
			ctx, project.ResourceGroupName, project.Parent.Name, project.Name, "azd-appinsights-"+project.Name, nil)
		if legacyErr == nil {
			return monitoringCleanup{}, fmt.Errorf("a legacy monitoring connection remains; " +
				"preserve its resources or explicitly remove that connection before deleting the owned group")
		}
		if !isNotFound(legacyErr) {
			return monitoringCleanup{}, monitoringReadError("inspect legacy monitoring connection before cleanup", legacyErr)
		}
		return monitoringCleanup{ownsResources: componentID != ""}, nil
	}
	if err != nil {
		return monitoringCleanup{}, monitoringReadError("inspect the generated monitoring connection before cleanup", err)
	}
	if connection.Properties == nil {
		return monitoringCleanup{}, fmt.Errorf("monitoring connection cleanup metadata is unavailable")
	}
	properties := connection.Properties.GetConnectionPropertiesV2()
	if properties == nil || properties.Target == nil {
		return monitoringCleanup{}, fmt.Errorf("monitoring connection cleanup target is unavailable")
	}
	target, err := monitoringResourceID(*properties.Target, "Microsoft.Insights/components")
	inGroup := err == nil && p.monitoringResourceInOwnedGroup(target)
	if componentID == "" && !inGroup {
		// A compatible external connection remains outside adjunct-group ownership.
		return monitoringCleanup{}, nil
	}
	if !inGroup || (componentID != "" && !strings.EqualFold(componentID, target.String())) ||
		properties.Category == nil || !strings.EqualFold(string(*properties.Category), "AppInsights") ||
		properties.AuthType == nil || *properties.AuthType != armcognitiveservices.ConnectionAuthTypeAPIKey ||
		!p.monitoringOwnershipMatches(properties.Metadata) ||
		properties.Metadata["ResourceId"] == nil ||
		!strings.EqualFold(*properties.Metadata["ResourceId"], target.String()) ||
		(properties.IsSharedToAll != nil && *properties.IsSharedToAll) || len(properties.SharedUserList) != 0 ||
		connection.ID == nil || !strings.EqualFold(*connection.ID, project.String()+"/connections/"+name) {
		return monitoringCleanup{}, fmt.Errorf("the generated monitoring connection was modified; " +
			"refusing to delete it or its resource group")
	}
	component, found, err := p.monitoringResource(ctx, target, "2020-02-02")
	if err != nil {
		return monitoringCleanup{}, err
	}
	if found {
		if !p.monitoringOwnershipMatches(component.Tags) {
			return monitoringCleanup{}, fmt.Errorf("the generated monitoring component ownership was modified; refusing cleanup")
		}
		properties, err := monitoringProperties(component)
		if err != nil {
			return monitoringCleanup{}, err
		}
		workspaceID, _ := properties["WorkspaceResourceId"].(string)
		workspace, err := monitoringResourceID(workspaceID, "Microsoft.OperationalInsights/workspaces")
		if err != nil || !p.monitoringResourceInOwnedGroup(workspace) {
			return monitoringCleanup{}, fmt.Errorf("the generated monitoring workspace was modified; refusing cleanup")
		}
	}
	return monitoringCleanup{connectionName: name, ownsResources: true}, nil
}

func (p *FoundryProvisioningProvider) deleteMonitoringConnection(
	ctx context.Context, cleanup monitoringCleanup, progress grpcbroker.ProgressFunc,
) error {
	if cleanup.connectionName == "" {
		return nil
	}
	project, err := monitoringResourceID(p.existingProjectID, "Microsoft.CognitiveServices/accounts/projects")
	if err != nil || project.Parent == nil {
		return fmt.Errorf("monitoring cleanup requires the bound existing Foundry project")
	}
	client, err := armcognitiveservices.NewProjectConnectionsClient(project.SubscriptionID, p.credential, p.armClientOptions)
	if err != nil {
		return err
	}
	progress(fmt.Sprintf("Deleting Foundry project connection %s...", cleanup.connectionName))
	_, err = client.Delete(
		ctx, project.ResourceGroupName, project.Parent.Name, project.Name, cleanup.connectionName, nil)
	if err != nil && !isNotFound(err) {
		return monitoringReadError("delete the generated monitoring connection", err)
	}
	return nil
}
