// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const contractClusterID = "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/" +
	"Microsoft.ContainerService/managedClusters/customer"

func hostingContractInput(extra string) Input {
	return Input{
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n    agentHosting:\n" +
			"      clusterResourceId: " + contractClusterID + "\n" + extra),
		ServiceName: "project", AcceptedHosts: []string{"azure.ai.project"},
	}
}

func hostingContractPrepared() PreparedHosting {
	const root = "/subscriptions/foundry-sub/resourceGroups/foundry-rg/providers/"
	return PreparedHosting{
		HostingManagementIdentityResourceID: root + "Microsoft.ManagedIdentity/userAssignedIdentities/hosting",
		WorkloadIdentityResourceID:          root + "Microsoft.ManagedIdentity/userAssignedIdentities/workload",
		StorageAccountResourceID:            root + "Microsoft.Storage/storageAccounts/storage",
		RegistryResourceID:                  root + "Microsoft.ContainerRegistry/registries/registry",
		AgentSubnetResourceID:               root + "Microsoft.Network/virtualNetworks/network/subnets/agents",
	}
}

func hostingContractParameters(t *testing.T, result *Result) map[string]any {
	t.Helper()
	data, err := json.Marshal(result.Parameters["agentHosting"])
	require.NoError(t, err)
	var hosting map[string]any
	require.NoError(t, json.Unmarshal(data, &hosting))
	return hosting
}

func TestOnboardingContractCompactPublicInputAndPreparedBoundary(t *testing.T) {
	for _, abac := range []bool{false, true} {
		t.Run(fmt.Sprintf("abac=%t", abac), func(t *testing.T) {
			input := hostingContractInput("")
			result, err := Synthesize(input)
			require.NoError(t, err)
			require.NotNil(t, result.Hosting)
			assert.Equal(t, AgentHostingInput{
				HostingType: "ManagedCluster", Name: "aks", ClusterResourceID: contractClusterID,
			}, *result.Hosting)
			public := *result.Hosting
			incomplete := hostingContractParameters(t, result)
			assert.Equal(t, contractClusterID, incomplete["clusterResourceId"])
			for _, field := range []string{
				"hostingManagementIdentityResourceId", "workloadIdentityResourceId",
				"storageAccountResourceId", "agentSubnetResourceId",
			} {
				assert.Empty(t, incomplete[field], "public synthesis must not invent prepared dependencies")
			}

			prepared := hostingContractPrepared()
			prepared.RegistryABAC = abac
			input.PreparedHosting = &prepared
			final, err := Synthesize(input)
			require.NoError(t, err)
			assert.Equal(t, public, *final.Hosting, "prepared IDs must not become customer input")
			hosting := hostingContractParameters(t, final)
			assert.Equal(t, true, hosting["enabled"])
			assert.Equal(t, "ManagedCluster", hosting["hostingType"])
			assert.Equal(t, "aks", hosting["name"])
			assert.Equal(t, contractClusterID, hosting["clusterResourceId"])
			for field, value := range map[string]string{
				"hostingManagementIdentityResourceId": prepared.HostingManagementIdentityResourceID,
				"workloadIdentityResourceId":          prepared.WorkloadIdentityResourceID,
				"storageAccountResourceId":            prepared.StorageAccountResourceID,
				"agentSubnetResourceId":               prepared.AgentSubnetResourceID,
			} {
				assert.Equal(t, value, hosting[field])
			}
			assert.Equal(t, prepared.RegistryResourceID, final.Parameters["containerRegistryResourceId"])
			assert.Equal(t, abac, final.Parameters["containerRegistryABAC"])
			assert.Equal(t, true, final.Parameters["includeAcr"])
			assert.Equal(t, incomplete, hostingContractParameters(t, result), "separate synthesis must not mutate preview")
		})
	}
}

func TestOnboardingContractRejectsIdentityPropertiesEvenWithPreparedInput(t *testing.T) {
	for _, field := range []string{"hostingManagementIdentityResourceId", "workloadIdentityResourceId"} {
		for _, value := range []string{"null", `""`, "~", "", `"${CUSTOMER_IDENTITY}"`} {
			t.Run(field+"/"+value, func(t *testing.T) {
				input := hostingContractInput("      " + field + ": " + value + "\n")
				prepared := hostingContractPrepared()
				input.PreparedHosting = &prepared
				result, err := Synthesize(input)
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Contains(t, err.Error(), field)
				assert.Contains(t, err.Error(), "remove")
			})
		}
	}
}

func TestOnboardingContractSuppliedChoicesAndExplicitDefaults(t *testing.T) {
	prepared := hostingContractPrepared()
	input := hostingContractInput(
		"      hostingType: ManagedCluster\n      name: customer-hosting\n" +
			"      storageAccountResourceId: " + prepared.StorageAccountResourceID + "\n" +
			"      agentSubnetResourceId: " + prepared.AgentSubnetResourceID + "\n",
	)
	input.PreparedHosting = &prepared
	result, err := Synthesize(input)
	require.NoError(t, err)
	require.NotNil(t, result.Hosting)
	assert.Equal(t, "customer-hosting", result.Hosting.Name)
	assert.Equal(t, prepared.StorageAccountResourceID, result.Hosting.StorageAccountResourceID)
	assert.Equal(t, prepared.AgentSubnetResourceID, result.Hosting.AgentSubnetResourceID)
	assert.Equal(t, "customer-hosting", hostingContractParameters(t, result)["name"])

	for _, field := range []string{"storageAccountResourceId", "agentSubnetResourceId", "hostingType", "name"} {
		for _, value := range []string{`""`, "null", `"   "`} {
			t.Run(field+"/"+value, func(t *testing.T) {
				input := hostingContractInput("      " + field + ": " + value + "\n")
				result, err := Synthesize(input)
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Contains(t, err.Error(), field)
			})
		}
	}
}

func TestOnboardingContractInvalidPreparedNeverMutatesResult(t *testing.T) {
	cases := map[string]func(*PreparedHosting){
		"missing hosting identity": func(p *PreparedHosting) { p.HostingManagementIdentityResourceID = "" },
		"missing workload identity": func(p *PreparedHosting) {
			p.WorkloadIdentityResourceID = ""
		},
		"same identity": func(p *PreparedHosting) {
			p.WorkloadIdentityResourceID = p.HostingManagementIdentityResourceID
		},
		"missing storage":  func(p *PreparedHosting) { p.StorageAccountResourceID = "" },
		"missing subnet":   func(p *PreparedHosting) { p.AgentSubnetResourceID = "" },
		"missing registry": func(p *PreparedHosting) { p.RegistryResourceID = "" },
		"changed storage":  func(p *PreparedHosting) { p.StorageAccountResourceID += "-other" },
		"changed subnet":   func(p *PreparedHosting) { p.AgentSubnetResourceID += "-other" },
	}
	for name, invalidate := range cases {
		t.Run(name, func(t *testing.T) {
			prepared := hostingContractPrepared()
			input := hostingContractInput(
				"      storageAccountResourceId: " + prepared.StorageAccountResourceID + "\n" +
					"      agentSubnetResourceId: " + prepared.AgentSubnetResourceID + "\n",
			)
			result, err := Synthesize(input)
			require.NoError(t, err)
			before, err := json.Marshal(result.Parameters)
			require.NoError(t, err)
			invalidate(&prepared)
			require.Error(t, ApplyPreparedHosting(result, prepared))
			after, err := json.Marshal(result.Parameters)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "failed final validation must be atomic")
		})
	}
}
