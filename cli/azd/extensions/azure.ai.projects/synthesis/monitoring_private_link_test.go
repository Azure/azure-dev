// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testVNetID = "/subscriptions/" + testSub + "/resourceGroups/net/providers/Microsoft.Network/virtualNetworks/vnet"
	testPEID   = "/subscriptions/" + testSub + "/resourceGroups/net/providers/Microsoft.Network/privateEndpoints/pe"
)

func privateLinkReader(group, scopeID string) *fakeReader {
	connection := func(groupID string) []any {
		return []any{map[string]any{"properties": map[string]any{
			"groupIds": []any{groupID}, "privateLinkServiceId": scopeID,
		}}}
	}
	return &fakeReader{resources: map[string]*armresources.GenericResource{
		strings.ToLower(testVNetID): armResource(testVNetID, map[string]any{"subnets": []any{
			map[string]any{"properties": map[string]any{"privateEndpoints": []any{map[string]any{"id": testPEID}}}},
		}}),
		strings.ToLower(testPEID): armResource(testPEID, map[string]any{
			"privateLinkServiceConnections": connection(group),
		}),
	}}
}

func TestMonitorPrivateLinkAccessModes(t *testing.T) {
	t.Parallel()
	// Matches the Foundry samples: ingestion PrivateOnly, query Open.
	for _, name := range []string{"templates/main.arm.json", "templates/existing-project.arm.json"} {
		raw, err := templatesFS.ReadFile(name)
		require.NoError(t, err)
		text := string(raw)
		assert.Contains(t, text, `"ingestionAccessMode": "PrivateOnly"`, name)
		assert.Contains(t, text, `"queryAccessMode": "Open"`, name)
		assert.NotContains(t, text, `"ingestionAccessMode": "Open"`, name)
		assert.NotContains(t, text, "azd-ampls-linked", name)
	}
	bicep, err := templatesFS.ReadFile("templates/modules/monitor-private-link.bicep")
	require.NoError(t, err)
	assert.Contains(t, string(bicep), "ingestionAccessMode: 'PrivateOnly'")
	assert.Contains(t, string(bicep), "queryAccessMode: 'Open'")
	assert.NotContains(t, string(bicep), "touchApplicationInsights")
}

func TestMonitoringResyncMatchesComponent(t *testing.T) {
	t.Parallel()
	read := func(name string) string {
		raw, err := templatesFS.ReadFile(name)
		require.NoError(t, err)
		return string(raw)
	}
	monitoring := read("templates/modules/monitoring.bicep")
	resync := read("templates/modules/monitoring-resync.bicep")
	// The second component update must carry the original properties, run only on
	// private networks, and be ordered after the scope through its output.
	assert.Contains(t, monitoring, "module resync 'monitoring-resync.bicep' = if (createResources && privateNetwork)")
	assert.Contains(t, monitoring, "privateLink!.outputs.azureMonitorPrivateLinkScopeResourceId")
	assert.Contains(t, monitoring, "resync!.outputs.id")
	assert.Contains(t, resync, "publicNetworkAccessForQuery: publicNetworkAccessForQuery")
	assert.Contains(t, resync, "uniqueString(privateLinkScopeResourceId, name)")
	assert.NotContains(t, resync, "utcNow")
	assert.Contains(t, monitoring, "publicNetworkAccessForQuery: 'Enabled'")
}

func TestMonitoringPrivateLinkScopesExistingComponent(t *testing.T) {
	t.Parallel()
	raw, err := templatesFS.ReadFile("templates/modules/monitoring.bicep")
	require.NoError(t, err)
	monitoring := string(raw)
	// A connected existing component is scoped to the new AMPLS, but never rewritten.
	assert.Contains(t, monitoring,
		"module privateLink 'monitor-private-link.bicep' = if ((createResources || mode == 'reuse-connect') && privateNetwork)")
	assert.Contains(t, monitoring, "createResources ? component!.id : existingComponent!.id")
	assert.Contains(t, monitoring,
		"createResources ? workspace!.id : existingComponent!.properties.WorkspaceResourceId")
	assert.Contains(t, monitoring, "module resync 'monitoring-resync.bicep' = if (createResources && privateNetwork)")
	assert.NotContains(t, monitoring, "resource existingComponent 'Microsoft.Insights/components@2020-02-02' = ")
}

func TestPrivateMonitoringTemplateGates(t *testing.T) {
	t.Parallel()
	read := func(name string) string {
		raw, err := templatesFS.ReadFile(name)
		require.NoError(t, err)
		return string(raw)
	}
	main := read("templates/main.bicep")
	// Without azd's verified-network approval the private path is never created; managed egress adds an outbound rule.
	assert.Contains(t, main, "param monitoringPrivateLinkApproved bool = false")
	assert.Contains(t, main, "enableNetworkIsolation && !monitoringPrivateLinkApproved")
	assert.Contains(t, main, "managedEgressRule: managedMonitoringRule")
	assert.Contains(t, main, "module monitoring 'modules/monitoring.bicep' = if (effectiveMonitoringMode != 'none')")
	assert.Contains(t, main, "output AZD_MONITORING_PRIVATE_LINK_SCOPE_ID")
	// The workspace closes public ingestion like the component does.
	monitoring := read("templates/modules/monitoring.bicep")
	assert.Contains(t, monitoring, "ampls-monitor-rule")
	assert.Contains(t, monitoring, "subresourceTarget: 'azuremonitor'")
	workspace := monitoring[strings.Index(monitoring, "resource workspace"):strings.Index(monitoring, "resource component")]
	assert.Contains(t, workspace, "publicNetworkAccessForIngestion: publicNetworkAccess")
	assert.Contains(t, workspace, "publicNetworkAccessForQuery: 'Enabled'")
}

func TestCheckMonitorPrivateLinkConflict(t *testing.T) {
	t.Parallel()
	const ownedScope = "/subscriptions/" + testSub + "/resourceGroups/rg/providers/Microsoft.Insights/privateLinkScopes/ampls-abc"
	const foreignScope = "/subscriptions/" + testSub + "/resourceGroups/shared/providers/Microsoft.Insights/privateLinkScopes/corp"
	const sameGroupForeign = "/subscriptions/" + testSub + "/resourceGroups/rg/providers/Microsoft.Insights/privateLinkScopes/corp"

	const lookalikeScope = "/subscriptions/" + testSub + "/resourceGroups/rg/providers/Microsoft.Insights/privateLinkScopes/ampls-other"
	const otherSubScope = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Insights/privateLinkScopes/ampls-abc"

	tests := []struct {
		name     string
		reader   *fakeReader
		owned    string
		conflict bool
		err      bool
	}{
		{name: "no private endpoints", reader: &fakeReader{resources: map[string]*armresources.GenericResource{
			strings.ToLower(testVNetID): armResource(testVNetID, map[string]any{}),
		}}},
		{name: "virtual network does not exist yet", reader: &fakeReader{}},
		{name: "unrelated private endpoint", reader: privateLinkReader("blob", foreignScope)},
		{name: "owned scope from a previous run", reader: privateLinkReader("azuremonitor", ownedScope), owned: ownedScope},
		{name: "owned scope matched case-insensitively", reader: privateLinkReader("azuremonitor", ownedScope),
			owned: strings.ToUpper(ownedScope)},
		{name: "scope not yet created by this project", reader: privateLinkReader("azuremonitor", ownedScope), conflict: true},
		{name: "ampls-prefixed scope of another project", reader: privateLinkReader("azuremonitor", lookalikeScope),
			owned: ownedScope, conflict: true},
		{name: "same scope name in another subscription", reader: privateLinkReader("azuremonitor", otherSubScope),
			owned: ownedScope, conflict: true},
		{name: "foreign scope", reader: privateLinkReader("azuremonitor", foreignScope), owned: ownedScope, conflict: true},
		{name: "foreign scope in the deployment group", reader: privateLinkReader("azuremonitor", sameGroupForeign),
			owned: ownedScope, conflict: true},
		{name: "read failure", reader: &fakeReader{resourceErr: errors.New("boom")}, err: true},
		{name: "private endpoint deleted since listing", reader: func() *fakeReader {
			r := privateLinkReader("azuremonitor", foreignScope)
			delete(r.resources, strings.ToLower(testPEID))
			return r
		}()},
		{name: "malformed private endpoint id", reader: &fakeReader{resources: map[string]*armresources.GenericResource{
			strings.ToLower(testVNetID): armResource(testVNetID, map[string]any{"subnets": []any{
				map[string]any{"properties": map[string]any{"privateEndpoints": []any{map[string]any{"id": "not-an-id"}}}},
			}}),
		}}, err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := (&MonitoringResolver{Reader: tt.reader}).CheckMonitorPrivateLinkConflict(t.Context(), testVNetID, tt.owned)
			var conflict *MonitorPrivateLinkConflictError
			switch {
			case tt.conflict:
				require.ErrorAs(t, err, &conflict)
				assert.Equal(t, testPEID, conflict.PrivateEndpointID)
				assert.Contains(t, err.Error(), "monitoring.enabled: false")
			case tt.err:
				require.Error(t, err)
				assert.False(t, errors.As(err, &conflict))
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestMonitoringKeyPolicyForPrivateNetwork(t *testing.T) {
	t.Parallel()
	disabled := map[string]any{"publicNetworkAccessForIngestion": "Disabled"}
	assert.Error(t, MonitoringKeyPolicyFor(disabled, false))
	assert.NoError(t, MonitoringKeyPolicyFor(disabled, true))
	assert.Error(t, MonitoringKeyPolicyFor(map[string]any{"DisableLocalAuth": true}, true))
}
