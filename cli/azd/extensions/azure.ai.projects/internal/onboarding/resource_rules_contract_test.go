// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/require"
)

type resourceRuleContractFixture struct {
	Cluster armcontainerservice.ManagedCluster
	Pools   []*armcontainerservice.AgentPool
	SKUs    []*armcompute.ResourceSKU
}

func resourceRuleContractJSON(t *testing.T, target any, raw string) {
	t.Helper()
	require.NoError(t, json.Unmarshal([]byte(raw), target))
}

func newResourceRuleContractFixture(t *testing.T) resourceRuleContractFixture {
	t.Helper()
	var f resourceRuleContractFixture
	resourceRuleContractJSON(t, &f, `{
		"Cluster": {
			"id": "/subscriptions/s/resourceGroups/r/providers/Microsoft.ContainerService/managedClusters/c",
			"location": "westus",
			"sku": {"name": "Base", "tier": "Free"},
			"identity": {"type": "SystemAssigned"},
			"properties": {
				"currentKubernetesVersion": "1.31.0",
				"provisioningState": "Succeeded",
				"networkProfile": {
					"networkPlugin": "azure",
					"networkPluginMode": "overlay",
					"networkDataplane": "cilium"
				},
				"identityProfile": {
					"kubeletidentity": {"objectId": "11111111-1111-4111-8111-111111111111"}
				},
				"oidcIssuerProfile": {"enabled": true, "issuerURL": "https://issuer.example.test/"},
				"securityProfile": {"workloadIdentity": {"enabled": true}}
			}
		},
		"Pools": [
			{
				"id": "system-pool",
				"name": "system",
				"properties": {
					"mode": "System",
					"vmSize": "Standard_E4s_v5",
					"count": 1,
					"provisioningState": "Succeeded"
				}
			},
			{
				"id": "agent-pool",
				"name": "agents",
				"properties": {
					"mode": "User",
					"osSKU": "AzureLinux",
					"vmSize": "Standard_D8as_v5",
					"count": 1,
					"workloadRuntime": "KataVmIsolation",
					"nodeImageVersion": "AKSAzureLinux-V3katagen2-202608.14.0",
					"nodeLabels": {"agents.ai.azure.com/sandbox-node": "true"},
					"securityProfile": {"sshAccess": "Disabled"},
					"provisioningState": "Succeeded"
				}
			}
		],
		"SKUs": [
			{
				"name": "Standard_E4s_v5",
				"resourceType": "virtualMachines",
				"locations": ["westus"],
				"capabilities": [
					{"name": "vCPUs", "value": "4"},
					{"name": "MemoryGB", "value": "32"}
				]
			},
			{
				"name": "Standard_D8as_v5",
				"resourceType": "virtualMachines",
				"locations": ["westus"],
				"capabilities": [
					{"name": "vCPUs", "value": "8"},
					{"name": "MemoryGB", "value": "32"}
				]
			}
		]
	}`)
	require.Len(t, f.Pools, 2)
	require.NotNil(t, f.Pools[0])
	require.NotNil(t, f.Pools[1])
	require.NotNil(t, f.Pools[0].Properties)
	require.NotNil(t, f.Pools[1].Properties)
	require.Len(t, f.SKUs, 2)
	return f
}

func resourceRuleContractCheck(t *testing.T, report Report, id string) Check {
	t.Helper()
	var matches []Check
	for _, check := range report {
		if check.ID == id {
			matches = append(matches, check)
		}
	}
	require.Len(t, matches, 1, "expected one %s result", id)
	return matches[0]
}

func TestResourceRuleContractNumericVersions(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		known, valid  bool
	}{
		{"minimum minor", "1.31", true, true},
		{"minimum patch", "1.31.0", true, true},
		{"older minor with larger patch", "1.30.999", true, false},
		{"newer minor", "1.32.0", true, true},
		{"newer major", "2.0.0", true, true},
		{"missing", "", false, false},
		{"missing minor", "1", false, false},
		{"prerelease needs explicit compatibility", "1.31.0-preview", false, false},
		{"extra component", "1.31.0.1", false, false},
		{"empty component", "1..0", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			known, valid := numericVersion(tc.version, []int{1, 31})
			require.Equal(t, tc.known, known)
			require.Equal(t, tc.valid, valid)
		})
	}
	for _, tc := range []struct {
		name, image  string
		known, valid bool
	}{
		{"exact baseline", "AKSAzureLinux-V3katagen2-202608.14.0", true, true},
		{"older day", "AKSAzureLinux-V3katagen2-202608.13.99", true, false},
		{"older month", "AKSAzureLinux-V3katagen2-202607.99.99", true, false},
		{"newer patch", "AKSAzureLinux-V3katagen2-202608.14.1", true, true},
		{"numeric month", "AKSAzureLinux-V3katagen2-202610.1.0", true, true},
		{"unknown image family", "AKSAzureLinux-V4katagen2-202610.1.0", false, false},
		{"incomplete image", "AKSAzureLinux-V3katagen2-202608.14", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			known, valid := agentImage(tc.image)
			require.Equal(t, tc.known, known)
			require.Equal(t, tc.valid, valid)
		})
	}
}

func TestResourceRuleContractSKUCapacity(t *testing.T) {
	for _, tc := range []struct {
		name, patch string
		want        Status
	}{
		{"exact capacity threshold", `{}`, Pass},
		{"one fewer CPU", `{"capabilities":[
			{"name":"vCPUs","value":"7"},{"name":"MemoryGB","value":"32"}]}`, Fail},
		{"below memory threshold", `{"capabilities":[
			{"name":"vCPUs","value":"8"},{"name":"MemoryGB","value":"31.99"}]}`, Fail},
		{"constrained available CPUs", `{"capabilities":[
			{"name":"vCPUs","value":"16"},{"name":"vCPUsAvailable","value":"4"},
			{"name":"MemoryGB","value":"64"}]}`, Fail},
		{"unknown CPU capacity", `{"capabilities":[{"name":"MemoryGB","value":"64"}]}`, Unverified},
		{"invalid capacity", `{"capabilities":[
			{"name":"vCPUs","value":"unknown"},{"name":"MemoryGB","value":"64"}]}`, Unverified},
		{"wrong resource type", `{"resourceType":"availabilitySets"}`, Unverified},
		{"other location", `{"locations":["eastus"]}`, Unverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResourceRuleContractFixture(t)
			resourceRuleContractJSON(t, f.SKUs[1], tc.patch)
			status, detail := skuCapacity(f.SKUs, "westus", "Standard_D8as_v5", 8, 32)
			require.Equal(t, tc.want, status, detail)
			require.NotEmpty(t, detail)
		})
	}
	t.Run("unrelated location does not contaminate exact match", func(t *testing.T) {
		f := newResourceRuleContractFixture(t)
		var unrelated armcompute.ResourceSKU
		resourceRuleContractJSON(t, &unrelated, `{
			"name":"Standard_D8as_v5","resourceType":"virtualMachines","locations":["eastus"]
		}`)
		skus := append([]*armcompute.ResourceSKU{&unrelated}, f.SKUs...)
		status, _ := skuCapacity(skus, "westus", "Standard_D8as_v5", 8, 32)
		require.Equal(t, Pass, status)
	})
}

func TestResourceRuleContractAKSRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, cluster, agent, id string
		want                     Status
	}{
		{"valid baseline including Free billing tier", `{}`, `{}`, "A01", Pass},
		{"Automatic product", `{"sku":{"name":"Automatic"}}`, `{}`, "A01", Fail},
		{"actual version below minimum", `{"properties":{"currentKubernetesVersion":"1.30.99",
			"kubernetesVersion":"1.36.3"}}`, `{}`, "A01", Fail},
		{"kubenet", `{"properties":{"networkProfile":{"networkPlugin":"kubenet",
			"networkPluginMode":null}}}`, `{}`, "A02", Fail},
		{"service principal cluster", `{"identity":{"type":"None"}}`, `{}`, "A03", Fail},
		{"empty kubelet object ID", `{"properties":{"identityProfile":{
			"kubeletidentity":{"objectId":""}}}}`, `{}`, "A03", Unverified},
		{"OIDC disabled", `{"properties":{"oidcIssuerProfile":{"enabled":false}}}`, `{}`, "A04", Fail},
		{"Workload Identity disabled", `{"properties":{"securityProfile":{
			"workloadIdentity":{"enabled":false}}}}`, `{}`, "A04", Fail},
		{"Azure Linux 3", `{}`, `{"osSKU":"AzureLinux3"}`, "A06", Pass},
		{"Ubuntu agent", `{}`, `{"osSKU":"Ubuntu"}`, "A06", Fail},
		{"wrong runtime", `{}`, `{"workloadRuntime":"OCIContainer"}`, "A06", Fail},
		{"zero nodes", `{}`, `{"count":0}`, "A06", Fail},
		{"old image", `{}`, `{"nodeImageVersion":"AKSAzureLinux-V3katagen2-202608.13.0"}`, "A07", Fail},
		{"unknown image", `{}`, `{"nodeImageVersion":"unknown-family-202610.1.0"}`, "A07", Unverified},
		{"empty label", `{}`, `{"nodeLabels":{"agents.ai.azure.com/sandbox-node":""}}`, "A08", Fail},
		{"wrong label value", `{}`, `{"nodeLabels":{"agents.ai.azure.com/sandbox-node":"True"}}`, "A08", Fail},
		{"SSH enabled", `{}`, `{"securityProfile":{"sshAccess":"Enabled"}}`, "A09", Fail},
		{"pool still updating", `{}`, `{"provisioningState":"Updating"}`, "A10", Fail},
		{"cluster still updating", `{"properties":{"provisioningState":"Updating"}}`, `{}`, "A10", Fail},
		{"Cilium enforcement", `{}`, `{}`, "A11", Pass},
		{"Azure enforcement", `{"properties":{"networkProfile":{"networkPolicy":"azure",
			"networkDataplane":"azure"}}}`, `{}`, "A11", Pass},
		{"Calico enforcement", `{"properties":{"networkProfile":{"networkPolicy":"calico",
			"networkDataplane":"azure"}}}`, `{}`, "A11", Pass},
		{"Azure dataplane without policy", `{"properties":{"networkProfile":{"networkPolicy":null,
			"networkDataplane":"azure"}}}`, `{}`, "A11", Fail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResourceRuleContractFixture(t)
			resourceRuleContractJSON(t, &f.Cluster, tc.cluster)
			resourceRuleContractJSON(t, f.Pools[1].Properties, tc.agent)
			_, report := aksResourceChecks(f.Cluster, f.Pools, f.SKUs)
			check := resourceRuleContractCheck(t, report, tc.id)
			require.Equal(t, tc.want, check.Status, check.Observed)
			require.NotEmpty(t, check.Scope)
			require.True(t, check.Mandatory)
		})
	}
	t.Run("complete baseline", func(t *testing.T) {
		f := newResourceRuleContractFixture(t)
		kubelet, report := aksResourceChecks(f.Cluster, f.Pools, f.SKUs)
		require.Equal(t, "11111111-1111-4111-8111-111111111111", kubelet)
		require.NoError(t, report.Err())
		require.Equal(t, Pass, resourceRuleContractCheck(t, report, "A05").Status)
	})
	t.Run("incomplete evidence on a mixed-size pool", func(t *testing.T) {
		f := newResourceRuleContractFixture(t)
		resourceRuleContractJSON(t, f.Pools[1].Properties,
			`{"virtualMachineNodesStatus":[{"size":"Standard_D8as_v5"},{"size":"Unknown_Size"}]}`)
		_, report := aksResourceChecks(f.Cluster, f.Pools, f.SKUs)
		require.Equal(t, Unverified, resourceRuleContractCheck(t, report, "A06").Status)
	})
	for _, tc := range []struct {
		name, id string
		mutate   func(*resourceRuleContractFixture)
		want     Status
	}{
		{"no kubelet identity metadata", "A03", func(f *resourceRuleContractFixture) {
			f.Cluster.Properties.IdentityProfile = nil
		}, Unverified},
		{"no persisted sandbox label", "A08", func(f *resourceRuleContractFixture) {
			f.Pools[1].Properties.NodeLabels = map[string]*string{}
		}, Fail},
		{"absent SSH profile is not disabled", "A09", func(f *resourceRuleContractFixture) {
			f.Pools[1].Properties.SecurityProfile = nil
		}, Unverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResourceRuleContractFixture(t)
			tc.mutate(&f)
			_, report := aksResourceChecks(f.Cluster, f.Pools, f.SKUs)
			require.Equal(t, tc.want, resourceRuleContractCheck(t, report, tc.id).Status)
		})
	}
}

func TestResourceRuleContractSameAgentPool(t *testing.T) {
	t.Run("do not combine per-pool passes", func(t *testing.T) {
		f := newResourceRuleContractFixture(t)
		other := newResourceRuleContractFixture(t).Pools[1]
		other.ID, other.Name = new("other-agent-pool"), new("other")
		resourceRuleContractJSON(t, f.Pools[1].Properties,
			`{"nodeLabels":{"agents.ai.azure.com/sandbox-node":"false"}}`)
		resourceRuleContractJSON(t, other.Properties, `{"count":0}`)
		f.Pools = append(f.Pools, other)
		_, report := aksResourceChecks(f.Cluster, f.Pools, f.SKUs)
		require.Error(t, report.Err(), "different pools must not supply different passing requirements")
		var scope string
		for _, id := range []string{"A06", "A07", "A08", "A09", "A10"} {
			check := resourceRuleContractCheck(t, report, id)
			if scope == "" {
				scope = check.Scope
			}
			require.Equal(t, scope, check.Scope)
		}
	})
	t.Run("Pod Subnet must belong to the qualifying agent pool", func(t *testing.T) {
		f := newResourceRuleContractFixture(t)
		f.Cluster.Properties.NetworkProfile.NetworkPluginMode = nil
		other := newResourceRuleContractFixture(t).Pools[1]
		other.ID, other.Name = new("non-agent-pool"), new("other")
		resourceRuleContractJSON(t, other.Properties,
			`{"workloadRuntime":"OCIContainer","podSubnetID":"/subscriptions/s/resourceGroups/r/providers/`+
				`Microsoft.Network/virtualNetworks/v/subnets/pods"}`)
		f.Pools = append(f.Pools, other)
		_, report := aksResourceChecks(f.Cluster, f.Pools, f.SKUs)
		require.NotEqual(t, Pass, resourceRuleContractCheck(t, report, "A02").Status)
	})
}

func newResourceRuleContractStorage(t *testing.T) armstorage.Account {
	t.Helper()
	var account armstorage.Account
	resourceRuleContractJSON(t, &account, `{
		"id":"storage-account","kind":"StorageV2",
		"properties":{
			"accessTier":"Hot","isHnsEnabled":false,"supportsHttpsTrafficOnly":true,
			"minimumTlsVersion":"TLS1_2","allowSharedKeyAccess":false,"allowBlobPublicAccess":false
		}
	}`)
	return account
}

func TestResourceRuleContractStorage(t *testing.T) {
	for _, tc := range []struct {
		name, patch, id string
		want            Status
	}{
		{"compliant existing account", `{}`, "S01", Pass},
		{"existing tags do not imply exclusive-use requirements",
			`{"tags":{"purpose":"shared-with-other-workloads"}}`, "S01", Pass},
		{"wrong kind", `{"kind":"BlobStorage"}`, "S01", Fail},
		{"Cool tier", `{"properties":{"accessTier":"Cool"}}`, "S01", Fail},
		{"HNS enabled", `{"properties":{"isHnsEnabled":true}}`, "S01", Fail},
		{"compliant transport", `{}`, "S02", Pass},
		{"TLS 1.3", `{"properties":{"minimumTlsVersion":"TLS1_3"}}`, "S02", Pass},
		{"TLS 1.1", `{"properties":{"minimumTlsVersion":"TLS1_1"}}`, "S02", Fail},
		{"unknown TLS evidence", `{"properties":{"minimumTlsVersion":"UNKNOWN"}}`, "S02", Unverified},
		{"HTTP enabled", `{"properties":{"supportsHttpsTrafficOnly":false}}`, "S02", Fail},
		{"Shared Key enabled", `{"properties":{"allowSharedKeyAccess":true}}`, "S02", Fail},
		{"anonymous Blob enabled", `{"properties":{"allowBlobPublicAccess":true}}`, "S02", Fail},
		{"public network access is not anonymous Blob access",
			`{"properties":{"publicNetworkAccess":"Enabled"}}`, "S02", Pass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := newResourceRuleContractStorage(t)
			resourceRuleContractJSON(t, &account, tc.patch)
			check := resourceRuleContractCheck(t, storageResourceChecks(account), tc.id)
			require.Equal(t, tc.want, check.Status, check.Observed)
		})
	}
	for _, tc := range []struct {
		name, id string
		mutate   func(*armstorage.AccountProperties)
		want     Status
	}{
		{"unknown HNS configuration", "S01", func(p *armstorage.AccountProperties) {
			p.IsHnsEnabled = nil
		}, Unverified},
		{"absent minimum TLS has an unsafe default", "S02", func(p *armstorage.AccountProperties) {
			p.MinimumTLSVersion = nil
		}, Fail},
		{"absent Shared Key has an unsafe default", "S02", func(p *armstorage.AccountProperties) {
			p.AllowSharedKeyAccess = nil
		}, Fail},
		{"documented HTTPS default", "S02", func(p *armstorage.AccountProperties) {
			p.EnableHTTPSTrafficOnly = nil
		}, Pass},
		{"documented anonymous Blob default", "S02", func(p *armstorage.AccountProperties) {
			p.AllowBlobPublicAccess = nil
		}, Pass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := newResourceRuleContractStorage(t)
			require.NotNil(t, account.Properties)
			tc.mutate(account.Properties)
			check := resourceRuleContractCheck(t, storageResourceChecks(account), tc.id)
			require.Equal(t, tc.want, check.Status, check.Observed)
		})
	}
}

func newResourceRuleContractRegistry(t *testing.T) armcontainerregistry.Registry {
	t.Helper()
	var registry armcontainerregistry.Registry
	resourceRuleContractJSON(t, &registry, `{
		"id":"registry",
		"properties":{
			"provisioningState":"Succeeded","loginServer":"registry.azurecr.io",
			"publicNetworkAccess":"Enabled","roleAssignmentMode":"LegacyRegistryPermissions"
		}
	}`)
	return registry
}

func TestResourceRuleContractRegistry(t *testing.T) {
	for _, tc := range []struct {
		name, patch string
		want        Status
		abac        bool
	}{
		{"ordinary RBAC", `{}`, Pass, false},
		{"ABAC mode", `{"properties":{"roleAssignmentMode":"AbacRepositoryPermissions"}}`, Pass, true},
		{"unknown future mode", `{"properties":{"roleAssignmentMode":"Unknown"}}`, Unverified, false},
		{"registry creating", `{"properties":{"provisioningState":"Creating"}}`, Fail, false},
		{"empty login endpoint", `{"properties":{"loginServer":""}}`, Unverified, false},
		{"disabled public access without private endpoint",
			`{"properties":{"publicNetworkAccess":"Disabled"}}`, Fail, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := newResourceRuleContractRegistry(t)
			resourceRuleContractJSON(t, &registry, tc.patch)
			abac, check := registryResourceCheck(registry)
			require.Equal(t, tc.want, check.Status, check.Observed)
			require.Equal(t, tc.abac, abac)
		})
	}
	t.Run("absent mode is not proof of ordinary RBAC", func(t *testing.T) {
		registry := newResourceRuleContractRegistry(t)
		require.NotNil(t, registry.Properties)
		registry.Properties.RoleAssignmentMode = nil
		_, check := registryResourceCheck(registry)
		require.Equal(t, Unverified, check.Status)
	})
	for _, state := range []string{"Pending", "Rejected", "Disconnected"} {
		t.Run("private-only connection "+state, func(t *testing.T) {
			var registry armcontainerregistry.Registry
			raw := `{
				"id":"registry",
				"properties":{
					"provisioningState":"Succeeded","loginServer":"registry.azurecr.io",
					"publicNetworkAccess":"Disabled","roleAssignmentMode":"LegacyRegistryPermissions",
					"privateEndpointConnections":[{"properties":{
						"privateLinkServiceConnectionState":{"status":"` + state + `"}
					}}]
				}
			}`
			resourceRuleContractJSON(t, &registry, raw)
			_, check := registryResourceCheck(registry)
			require.NotEqual(t, Pass, check.Status, "an unusable private-only connection is not ready access")
		})
	}
}
