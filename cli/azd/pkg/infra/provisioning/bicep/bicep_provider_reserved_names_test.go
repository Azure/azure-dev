// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindReservedResourceNameViolations(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		want         []reservedNameViolation
	}{
		{
			name:         "valid resource name",
			resourceName: "my-resource-name",
		},
		{
			name:         "empty resource name",
			resourceName: "",
		},
		{
			name:         "empty segments are skipped",
			resourceName: "/",
		},
		{
			name:         "exact match reserved word",
			resourceName: "azure",
			want: []reservedNameViolation{
				{segment: "azure", reservedWord: "AZURE", matchType: "exactly matches"},
			},
		},
		{
			name:         "substring reserved word",
			resourceName: "project-MicrosoftLearnAgent",
			want: []reservedNameViolation{
				{segment: "project-MicrosoftLearnAgent", reservedWord: "MICROSOFT", matchType: "contains"},
			},
		},
		{
			name:         "prefix reserved word",
			resourceName: "LoginPortal",
			want: []reservedNameViolation{
				{segment: "LoginPortal", reservedWord: "LOGIN", matchType: "starts with"},
			},
		},
		{
			name:         "checks last segment only — earlier segments are scanned via parent resource",
			resourceName: "ai-account/AZURE",
			want: []reservedNameViolation{
				{segment: "AZURE", reservedWord: "AZURE", matchType: "exactly matches"},
			},
		},
		{
			name:         "checks child resource segment",
			resourceName: "ai-account/project-MicrosoftLearnAgent",
			want: []reservedNameViolation{
				{segment: "project-MicrosoftLearnAgent", reservedWord: "MICROSOFT", matchType: "contains"},
			},
		},
		{
			name:         "reports multiple violations in single segment",
			resourceName: "LoginMicrosoftApp",
			want: []reservedNameViolation{
				{segment: "LoginMicrosoftApp", reservedWord: "LOGIN", matchType: "starts with"},
				{segment: "LoginMicrosoftApp", reservedWord: "MICROSOFT", matchType: "contains"},
			},
		},
		{
			// A reserved word in a parent segment is reported only once — when
			// the parent resource is enumerated separately in the snapshot. The
			// child compound name does not duplicate it on its own pass.
			name:         "ignores reserved words in parent segments of a compound child name",
			resourceName: "Azure/LoginPortal",
			want: []reservedNameViolation{
				{segment: "LoginPortal", reservedWord: "LOGIN", matchType: "starts with"},
			},
		},
		{
			// Reproduces the issue #7805 child-link case. Even ignoring the
			// type-level exemption, only the trailing link segment is scanned.
			name:         "child link compound name only scans the link segment",
			resourceName: "privatelink.search.windows.net/privatelink.search.windows.net-link",
			want: []reservedNameViolation{
				{
					segment:      "privatelink.search.windows.net-link",
					reservedWord: "WINDOWS",
					matchType:    "contains",
				},
			},
		},
		{
			name: "skips unresolved ARM expression containing provider namespaces",
			resourceName: "[guid('/subscriptions/sub-id/resourceGroups/" +
				"rg-learn-agent-dev/providers/Microsoft.ContainerRegistry/" +
				"registries/cr123')]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findReservedResourceNameViolations(tt.resourceName)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCheckReservedResourceNames(t *testing.T) {
	provider := &BicepProvider{}

	results, err := provider.checkReservedResourceNames(t.Context(), &validationContext{
		SnapshotResources: []armTemplateResource{
			{
				Type: "Microsoft.CognitiveServices/accounts/projects",
				Name: "ai-account/project-MicrosoftLearnAgent",
			},
			{
				Type: "Microsoft.Web/sites",
				Name: "LoginPortal",
			},
			{
				// Triggers both LOGIN prefix and MICROSOFT substring rules in
				// a single segment — both should be reported as separate results.
				Type: "Microsoft.Web/sites",
				Name: "LoginMicrosoftApp",
			},
			{
				Type: "Microsoft.Storage/storageAccounts",
				Name: "validname",
			},
			{
				// Unresolved ARM expression — should be skipped even though it
				// contains provider namespaces like "Microsoft.ContainerRegistry".
				Type: "Microsoft.Authorization/roleAssignments",
				Name: "[guid('/subscriptions/sub-id/resourceGroups/rg-learn-agent-dev/providers/" +
					"Microsoft.ContainerRegistry/registries/cr123', principalId, roleDefId)]",
			},
			// --- Issue #7805 false-positives: exempt resource types should
			// produce no warnings even when the name contains "WINDOWS".
			{
				Type: "Microsoft.Network/privateDnsZones",
				Name: "privatelink.search.windows.net",
			},
			{
				Type: "Microsoft.Network/privateDnsZones/virtualNetworkLinks",
				Name: "privatelink.search.windows.net/privatelink.search.windows.net-link",
			},
			{
				Type: "Microsoft.Network/privateDnsZones/A",
				Name: "privatelink.redis.cache.windows.net/myrecord",
			},
			{
				Type: "Microsoft.Network/dnsZones",
				Name: "contoso.windows.net",
			},
			{
				Type: "Microsoft.Network/dnsForwardingRulesets/forwardingRules",
				Name: "ruleset/forward-windows-net",
			},
			{
				Type: "Microsoft.Network/dnsResolvers/inboundEndpoints",
				Name: "resolver/windows-inbound",
			},
			{
				Type: "Microsoft.Network/firewallPolicies/ruleCollectionGroups",
				Name: "policy/AllowMicrosoftServices",
			},
			{
				// Internal-only names are exempt: nested deployments commonly
				// embed reserved words like "microsoft" in their names.
				Type: "Microsoft.Resources/deployments",
				Name: "deploy-microsoft-graph",
			},
			{
				Type: "Microsoft.Authorization/roleDefinitions",
				Name: "MicrosoftAdminRole",
			},
			{
				Type: "Microsoft.Authorization/policyAssignments",
				Name: "audit-microsoft-services",
			},
			{
				Type: "Microsoft.Authorization/locks",
				Name: "DoNotDeleteMicrosoftSubscriptionLock",
			},
			{
				Type: "Microsoft.Insights/diagnosticSettings",
				Name: "audit-microsoft-events",
			},
			// Reproduces the false positive seen in the official azd sample
			// `Azure-Samples/azure-search-openai-demo` — NSG security rules
			// frequently encode Azure service-tag names like
			// "MicrosoftContainerRegistry" in their labels.
			{
				Type: "Microsoft.Network/networkSecurityGroups/securityRules",
				Name: "myNsg/AllowMicrosoftContainerRegistryOutbound",
			},
			// Resource group names are commonly derived from the user's azd
			// environment name (e.g. `rg-${environmentName}`); a project named
			// "microsoft-graph-svc" must not produce an unfixable warning.
			{
				Type: "Microsoft.Resources/resourceGroups",
				Name: "rg-microsoft-graph-svc",
			},
			// Key Vault item children: user-labeled, no endpoint enforcement.
			{
				Type: "Microsoft.KeyVault/vaults/secrets",
				Name: "kv/microsoft-graph-client-secret",
			},
			{
				Type: "Microsoft.KeyVault/vaults/keys",
				Name: "kv/windows-signing-key",
			},
			{
				Type: "Microsoft.KeyVault/vaults/certificates",
				Name: "kv/microsoftAuthCert",
			},
			{
				Type: "Microsoft.KeyVault/vaults/accessPolicies",
				Name: "kv/MicrosoftAdminPolicy",
			},
			// VM extension names follow the publisher.type convention; the
			// official Microsoft Sentinel azd template uses
			// "Microsoft.Insights.LogAnalyticsAgent".
			{
				Type: "Microsoft.Compute/virtualMachines/extensions",
				Name: "myVm/Microsoft.Insights.LogAnalyticsAgent",
			},
			// Logic Apps API connection: the resource name mirrors the
			// connector type by convention, including the literal "office365"
			// (which would otherwise hit the OFFICE365 reserved-word rule).
			{
				Type: "Microsoft.Web/connections",
				Name: "office365",
			},
			// Log Analytics workspace data source: user-labeled config child
			// frequently named after Windows perf counters.
			{
				Type: "Microsoft.OperationalInsights/workspaces/dataSources",
				Name: "myWorkspace/windowsPerfCounter1",
			},
			// Subnet delegations are named after the delegated provider's
			// service tag (e.g. "Microsoft.Web/serverFarms"); every Microsoft
			// delegation hits the MICROSOFT rule.
			{
				Type: "Microsoft.Network/virtualNetworks/subnets/delegations",
				Name: "vnet/snet/Microsoft.Web.serverFarms",
			},
			{
				// Case-insensitive type matching: ARM resource types are
				// case-insensitive, so a lower-cased provider should still be
				// recognized as exempt.
				Type: "microsoft.network/privatednszones",
				Name: "another.windows.net",
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, results, 4)

	for _, r := range results {
		require.Equal(t, PreflightCheckWarning, r.Severity)
		require.Equal(t, "reserved_resource_name", r.DiagnosticID)
	}

	// Child resource violation.
	require.Contains(t, results[0].Message, `"ai-account/project-MicrosoftLearnAgent"`)
	require.Contains(t, results[0].Message, "contains")
	require.Contains(t, results[0].Message, `"MICROSOFT"`)

	// Top-level resource violation.
	require.Contains(t, results[1].Message, `"LoginPortal"`)
	require.Contains(t, results[1].Message, "starts with")
	require.Contains(t, results[1].Message, `"LOGIN"`)

	// Both violations on LoginMicrosoftApp should be reported as distinct results.
	require.Contains(t, results[2].Message, `"LoginMicrosoftApp"`)
	require.Contains(t, results[2].Message, "starts with")
	require.Contains(t, results[2].Message, `"LOGIN"`)
	require.Contains(t, results[3].Message, `"LoginMicrosoftApp"`)
	require.Contains(t, results[3].Message, "contains")
	require.Contains(t, results[3].Message, `"MICROSOFT"`)
}

func TestIsReservedNameCheckExempt(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		want         bool
	}{
		{
			name:         "exact exempt parent type",
			resourceType: "Microsoft.Network/privateDnsZones",
			want:         true,
		},
		{
			name:         "exempt child via prefix match",
			resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks",
			want:         true,
		},
		{
			name:         "exempt grandchild via prefix match",
			resourceType: "Microsoft.Network/privateDnsZones/A/foo",
			want:         true,
		},
		{
			name:         "case-insensitive comparison",
			resourceType: "microsoft.network/privatednszones",
			want:         true,
		},
		{
			name:         "public dnsZones exempt",
			resourceType: "Microsoft.Network/dnsZones",
			want:         true,
		},
		{
			name:         "dns forwarding rulesets exempt",
			resourceType: "Microsoft.Network/dnsForwardingRulesets/forwardingRules",
			want:         true,
		},
		{
			name:         "dns resolvers inbound exempt",
			resourceType: "Microsoft.Network/dnsResolvers/inboundEndpoints",
			want:         true,
		},
		{
			name:         "firewall policy parent exempt",
			resourceType: "Microsoft.Network/firewallPolicies",
			want:         true,
		},
		{
			name:         "firewall policy rule collection groups exempt via prefix",
			resourceType: "Microsoft.Network/firewallPolicies/ruleCollectionGroups",
			want:         true,
		},
		{
			name:         "nested deployments exempt",
			resourceType: "Microsoft.Resources/deployments",
			want:         true,
		},
		{
			name:         "deployment scripts exempt",
			resourceType: "Microsoft.Resources/deploymentScripts",
			want:         true,
		},
		{
			name:         "role assignments exempt",
			resourceType: "Microsoft.Authorization/roleAssignments",
			want:         true,
		},
		{
			name:         "policy set definitions exempt",
			resourceType: "Microsoft.Authorization/policySetDefinitions",
			want:         true,
		},
		{
			name:         "authorization locks exempt",
			resourceType: "Microsoft.Authorization/locks",
			want:         true,
		},
		{
			name:         "diagnostic settings exempt",
			resourceType: "Microsoft.Insights/diagnosticSettings",
			want:         true,
		},
		{
			name:         "resource groups exempt",
			resourceType: "Microsoft.Resources/resourceGroups",
			want:         true,
		},
		{
			name:         "NSG security rules exempt via parent prefix",
			resourceType: "Microsoft.Network/networkSecurityGroups/securityRules",
			want:         true,
		},
		{
			name:         "key vault secrets exempt",
			resourceType: "Microsoft.KeyVault/vaults/secrets",
			want:         true,
		},
		{
			name:         "key vault keys exempt",
			resourceType: "Microsoft.KeyVault/vaults/keys",
			want:         true,
		},
		{
			name:         "key vault certificates exempt",
			resourceType: "Microsoft.KeyVault/vaults/certificates",
			want:         true,
		},
		{
			name:         "key vault accessPolicies exempt",
			resourceType: "Microsoft.KeyVault/vaults/accessPolicies",
			want:         true,
		},
		{
			name:         "VM extension exempt (publisher.type names)",
			resourceType: "Microsoft.Compute/virtualMachines/extensions",
			want:         true,
		},
		{
			name:         "VMSS extension exempt",
			resourceType: "Microsoft.Compute/virtualMachineScaleSets/extensions",
			want:         true,
		},
		{
			name:         "Logic Apps API connection exempt (e.g. office365)",
			resourceType: "Microsoft.Web/connections",
			want:         true,
		},
		{
			name:         "Log Analytics workspace data source exempt",
			resourceType: "Microsoft.OperationalInsights/workspaces/dataSources",
			want:         true,
		},
		{
			name:         "subnet delegation exempt",
			resourceType: "Microsoft.Network/virtualNetworks/subnets/delegations",
			want:         true,
		},
		{
			// VM parent is NOT exempt — only the per-extension child names are.
			name:         "VM parent NOT exempt",
			resourceType: "Microsoft.Compute/virtualMachines",
			want:         false,
		},
		{
			// VNet parent is NOT exempt — only the per-delegation child names are.
			name:         "VNet parent NOT exempt",
			resourceType: "Microsoft.Network/virtualNetworks",
			want:         false,
		},
		{
			// Parent KeyVault is NOT exempt — vault names are FQDNs
			// (<name>.vault.azure.net) and ARM enforces reserved-word checks.
			name:         "key vault parent NOT exempt",
			resourceType: "Microsoft.KeyVault/vaults",
			want:         false,
		},
		{
			// NSG names themselves are user-labeled (no FQDN, no public
			// endpoint), so the parent type is exempt too.
			name:         "NSG parent exempt",
			resourceType: "Microsoft.Network/networkSecurityGroups",
			want:         true,
		},
		{
			name:         "NSG security rule child exempt via prefix",
			resourceType: "Microsoft.Network/networkSecurityGroups/securityRules",
			want:         true,
		},
		{
			name:         "non-exempt type: storage accounts",
			resourceType: "Microsoft.Storage/storageAccounts",
			want:         false,
		},
		{
			name:         "non-exempt type: web sites",
			resourceType: "Microsoft.Web/sites",
			want:         false,
		},
		{
			// Type prefix without "/" boundary should not match (no false
			// matches via substring).
			name:         "prefix without slash boundary not exempt",
			resourceType: "Microsoft.Network/privateDnsZonesAndMore",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isReservedNameCheckExempt(tt.resourceType))
		})
	}
}
