// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
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

func hostingContractParameters(t *testing.T, result *Result) map[string]any {
	t.Helper()
	data, err := json.Marshal(result.Parameters["agentHosting"])
	require.NoError(t, err)
	var hosting map[string]any
	require.NoError(t, json.Unmarshal(data, &hosting))
	return hosting
}

func TestOnboardingContractCompactPublicInput(t *testing.T) {
	result, err := Synthesize(hostingContractInput(""))
	require.NoError(t, err)
	require.NotNil(t, result.Hosting)
	assert.Equal(t, AgentHostingInput{
		HostingType: "ManagedCluster", Name: "aks", ClusterResourceID: contractClusterID,
	}, *result.Hosting)
	hosting := hostingContractParameters(t, result)
	assert.Equal(t, true, hosting["enabled"])
	assert.Equal(t, "ManagedCluster", hosting["hostingType"])
	assert.Equal(t, "aks", hosting["name"])
	assert.Equal(t, contractClusterID, hosting["clusterResourceId"])
	for _, field := range []string{
		"hostingManagementIdentityResourceId", "workloadIdentityResourceId", "storageAccountResourceId",
	} {
		assert.Empty(t, hosting[field], "public synthesis must not invent deployment dependencies")
	}
	assert.Equal(t, true, result.Parameters["includeAcr"])
}

func TestOnboardingContractUsesStandardYAMLDecoding(t *testing.T) {
	for _, extra := range []string{
		"      unknown: ignored\n",
		"      storageAccountResourceId: null\n      hostingType: null\n      name: \"\"\n",
		"      storageAccountResourceId: \"\"\n",
	} {
		t.Run(extra, func(t *testing.T) {
			input := hostingContractInput(extra)
			result, err := Synthesize(input)
			require.NoError(t, err)
			assert.Equal(t, "aks", result.Hosting.Name)
			assert.Equal(t, managedClusterHostingType, result.Hosting.HostingType)
			assert.Empty(t, hostingContractParameters(t, result)["hostingManagementIdentityResourceId"])
		})
	}
	_, err := Synthesize(hostingContractInput("      name: first\n      name: second\n"))
	require.ErrorContains(t, err, "already defined", "duplicate keys retain the YAML library's behavior")
	result, err := Synthesize(hostingContractInput("      name: 123\n"))
	require.NoError(t, err, "standard YAML scalar-to-string decoding is retained")
	assert.Equal(t, "123", result.Hosting.Name)
}

func TestOnboardingContractSuppliedChoicesAndExplicitDefaults(t *testing.T) {
	plan := onboardingContractPlan()
	input := hostingContractInput(
		"      hostingType: ManagedCluster\n      name: customer-hosting\n" +
			"      storageAccountResourceId: " + plan.StorageAccountResourceID + "\n",
	)
	result, err := Synthesize(input)
	require.NoError(t, err)
	require.NotNil(t, result.Hosting)
	assert.Equal(t, "customer-hosting", result.Hosting.Name)
	assert.Equal(t, plan.StorageAccountResourceID, result.Hosting.StorageAccountResourceID)
	assert.Equal(t, "customer-hosting", hostingContractParameters(t, result)["name"])

	for _, field := range []string{"storageAccountResourceId", "hostingType", "name"} {
		for _, value := range []string{`"   "`} {
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
