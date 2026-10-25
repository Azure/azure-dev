// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStandardNetworkARMAndSchemaMatchPreBYOBaseline(t *testing.T) {
	raw, err := os.ReadFile("testdata/standard-network-before-byo.json")
	require.NoError(t, err)
	var baseline map[string]any
	require.NoError(t, json.Unmarshal(raw, &baseline))
	raw, err = ARMTemplate()
	require.NoError(t, err)
	var arm map[string]any
	require.NoError(t, json.Unmarshal(raw, &arm))
	object := func(value any) map[string]any {
		t.Helper()
		result, ok := value.(map[string]any)
		require.True(t, ok)
		return result
	}
	rootResources := object(arm["resources"])
	resourceModule := object(rootResources["resources"])
	moduleProperties := object(resourceModule["properties"])
	module := object(moduleProperties["template"])
	for category, actual := range map[string]map[string]any{
		"topLevelParameters": object(arm["parameters"]),
		"parameterHandoff":   object(moduleProperties["parameters"]),
		"variables":          object(module["variables"]),
		"accountProperties":  object(object(object(module["resources"])["foundryAccount"])["properties"]),
		"resources":          object(module["resources"]),
		"outputs":            object(arm["outputs"]),
	} {
		t.Run(category, func(t *testing.T) {
			for key, expected := range object(baseline[category]) {
				value := actual[key]
				if category == "resources" {
					value = withoutCompilerMetadata(value)
				}
				assert.Equal(t, expected, value, key)
			}
		})
	}
	raw, err = os.ReadFile(schemaPath)
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(raw, &schema))
	assert.Equal(t, baseline["networkSchema"], object(schema["properties"])["network"])
	assert.Equal(t, baseline["subnetSchema"], object(schema["definitions"])["Subnet"])
}

func withoutCompilerMetadata(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, entry := range value {
			if key != "metadata" {
				result[key] = withoutCompilerMetadata(entry)
			}
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, entry := range value {
			result[i] = withoutCompilerMetadata(entry)
		}
		return result
	default:
		return value
	}
}

func TestStandardNetworkSelectionUnaffectedByOnboarding(t *testing.T) {
	const vnet = "/subscriptions/network-sub/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/network"
	for _, choice := range []string{"none", "managed", "existing customer subnet", "create customer and PE subnets"} {
		t.Run(choice, func(t *testing.T) {
			network := ""
			expected := map[string]any{
				"enableNetworkIsolation": false, "useManagedEgress": false, "vnetId": "",
				"agentSubnetName": "agent-subnet", "agentSubnetPrefix": "", "createAgentSubnet": false,
				"peSubnetName": "pe-subnet", "peSubnetPrefix": "", "createPESubnet": false,
				"managedIsolationMode": "", "dnsZonesResourceGroup": "", "dnsZonesSubscription": "",
			}
			if choice != "none" {
				network = "    network:\n      peSubnet: {vnet: " + vnet + ", name: pe}\n"
				expected["enableNetworkIsolation"], expected["useManagedEgress"] = true, true
				expected["vnetId"], expected["peSubnetName"] = vnet, "pe"
			}
			if strings.Contains(choice, "customer") {
				network += "      agentSubnet: {vnet: " + vnet + ", name: agents}\n"
				expected["useManagedEgress"], expected["agentSubnetName"] = false, "agents"
			}
			if strings.HasPrefix(choice, "create") {
				network = strings.Replace(network, "name: agents}", "name: agents, prefix: 10.0.1.0/24}", 1)
				network = strings.Replace(network, "name: pe}", "name: pe, prefix: 10.0.2.0/24}", 1)
				expected["createAgentSubnet"], expected["agentSubnetPrefix"] = true, "10.0.1.0/24"
				expected["createPESubnet"], expected["peSubnetPrefix"] = true, "10.0.2.0/24"
			}
			standard, err := Synthesize(Input{
				RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n" + network),
				ServiceName:  "project",
			})
			require.NoError(t, err)
			byoInput := hostingContractInput("")
			byoInput.RawAzureYAML = append(byoInput.RawAzureYAML, []byte(network)...)
			byo, err := Synthesize(byoInput)
			require.NoError(t, err)
			for _, result := range []*Result{standard, byo} {
				for key, value := range expected {
					assert.Equal(t, value, result.Parameters[key], key)
				}
				assert.Equal(t, standard.NetworkMode, result.NetworkMode)
			}
			require.NoError(t, ApplyBYOOnboarding(byo, onboardingContractPlan()))
			for key, value := range expected {
				assert.Equal(t, value, byo.Parameters[key], "binding the graph must preserve "+key)
			}
		})
	}
}
