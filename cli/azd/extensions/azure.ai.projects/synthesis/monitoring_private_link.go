// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// ownedPrivateLinkScopePrefix is the name prefix monitor-private-link.bicep and
// the Terraform equivalent give the Azure Monitor Private Link Scope they create.
const ownedPrivateLinkScopePrefix = "ampls-"

// MonitorPrivateLinkConflictError reports an Azure Monitor private endpoint that
// already serves the VNet. A VNet can use only one Azure Monitor Private Link
// Scope; a second one overrides the shared Azure Monitor DNS zones.
type MonitorPrivateLinkConflictError struct {
	PrivateEndpointID string
}

func (e *MonitorPrivateLinkConflictError) Error() string {
	return fmt.Sprintf(
		"the virtual network already has an Azure Monitor private endpoint (%s); a network can use only one "+
			"Azure Monitor Private Link Scope, so automatic monitoring would override its DNS; "+
			"set monitoring.enabled: false on the project service",
		e.PrivateEndpointID)
}

// CheckMonitorPrivateLinkConflict looks for an existing azuremonitor private
// endpoint on the VNet that this deployment does not own. It is read-only. A
// *MonitorPrivateLinkConflictError means a conflict; any other error means the
// check could not complete and ARM remains the authority.
func (p *MonitoringResolver) CheckMonitorPrivateLinkConflict(
	ctx context.Context, vnetID, ownedResourceGroup string,
) error {
	id, err := MonitoringResourceID(vnetID, "Microsoft.Network/virtualNetworks")
	if err != nil {
		return err
	}
	vnet, found, err := p.Resource(ctx, id, "2023-09-01")
	if err != nil || !found {
		return err
	}
	for _, endpointID := range vnetPrivateEndpointIDs(vnet) {
		parsed, err := MonitoringResourceID(endpointID, "Microsoft.Network/privateEndpoints")
		if err != nil {
			return err
		}
		endpoint, found, err := p.Resource(ctx, parsed, "2023-09-01")
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		scopeID, isMonitor := azureMonitorLinkTarget(endpoint)
		if !isMonitor {
			continue
		}
		if scope, err := arm.ParseResourceID(scopeID); err == nil &&
			strings.EqualFold(scope.ResourceGroupName, ownedResourceGroup) &&
			strings.HasPrefix(strings.ToLower(scope.Name), ownedPrivateLinkScopePrefix) {
			continue
		}
		return &MonitorPrivateLinkConflictError{PrivateEndpointID: endpointID}
	}
	return nil
}

func vnetPrivateEndpointIDs(vnet *armresources.GenericResource) []string {
	properties, _ := vnet.Properties.(map[string]any)
	subnets, _ := properties["subnets"].([]any)
	var ids []string
	for _, subnet := range subnets {
		subnetProperties, _ := subnet.(map[string]any)["properties"].(map[string]any)
		endpoints, _ := subnetProperties["privateEndpoints"].([]any)
		for _, endpoint := range endpoints {
			if id, _ := endpoint.(map[string]any)["id"].(string); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// azureMonitorLinkTarget returns the private link service id when the endpoint
// connects to Azure Monitor (group id azuremonitor).
func azureMonitorLinkTarget(endpoint *armresources.GenericResource) (string, bool) {
	properties, _ := endpoint.Properties.(map[string]any)
	for _, key := range []string{"privateLinkServiceConnections", "manualPrivateLinkServiceConnections"} {
		connections, _ := properties[key].([]any)
		for _, connection := range connections {
			connectionProperties, _ := connection.(map[string]any)["properties"].(map[string]any)
			groups, _ := connectionProperties["groupIds"].([]any)
			for _, group := range groups {
				if name, _ := group.(string); strings.EqualFold(name, "azuremonitor") {
					target, _ := connectionProperties["privateLinkServiceId"].(string)
					return target, true
				}
			}
		}
	}
	return "", false
}
