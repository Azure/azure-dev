// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type monitoringConnection struct {
	name   string
	target string
}

func monitoringResourceID(value, resourceType string) (*arm.ResourceID, error) {
	return synthesis.MonitoringResourceID(value, resourceType)
}

func monitoringReadError(operation string, err error) error {
	return synthesis.MonitoringReadError(operation, err)
}

func monitoringProperties(resource *armresources.GenericResource) (map[string]any, error) {
	return synthesis.MonitoringProperties(resource)
}

func monitoringKeyPolicy(properties map[string]any) error {
	return synthesis.MonitoringKeyPolicy(properties)
}

func monitoringNetworkPolicy(properties map[string]any) error {
	return synthesis.MonitoringNetworkPolicy(properties)
}

func (p *FoundryProvisioningProvider) monitoringResolver() *synthesis.MonitoringResolver {
	return &synthesis.MonitoringResolver{Credential: p.credential, ClientOptions: p.armClientOptions}
}

func (p *FoundryProvisioningProvider) monitoringResource(
	ctx context.Context, id *arm.ResourceID, version string,
) (*armresources.GenericResource, bool, error) {
	return p.monitoringResolver().Resource(ctx, id, version)
}

func (p *FoundryProvisioningProvider) discoverMonitoringConnection(
	ctx context.Context, project *arm.ResourceID, projectExists, accountExists bool,
) (*monitoringConnection, error) {
	connection, err := p.monitoringResolver().DiscoverConnection(ctx, project, projectExists, accountExists)
	if err != nil || connection == nil {
		return nil, err
	}
	return &monitoringConnection{name: connection.Name, target: connection.Target}, nil
}

func (p *FoundryProvisioningProvider) validateMonitoringComponent(
	ctx context.Context, componentID string, needsKey bool,
) error {
	return p.monitoringResolver().ValidateComponent(ctx, componentID, needsKey)
}
