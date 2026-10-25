// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyNetworkDoesNotAddBYOPermissionPrechecks(t *testing.T) {
	const vnet = "/subscriptions/network-sub/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/network"
	for _, choice := range []string{"none", "managed", "reference", "create", "existing DNS"} {
		t.Run(choice, func(t *testing.T) {
			input := contractPlanInput()
			input.AgentSubnetID = ""
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			baseline, err := WithDeploymentOperations(plan, DeploymentSelection{})
			require.NoError(t, err)
			selection := DeploymentSelection{}
			if choice != "none" {
				selection.Network = &LegacyNetwork{
					Enabled: true, VNetID: vnet, AgentSubnetName: "legacy", PESubnetName: "pe",
					ManagedEgress:     choice == "managed",
					CreateAgentSubnet: choice == "create", CreatePESubnet: choice == "create",
				}
				if choice == "existing DNS" {
					selection.Network.DNSResourceGroup, selection.Network.DNSSubscription = "dns-rg", "dns-sub"
				}
			}
			plan, err = WithDeploymentOperations(plan, selection)
			require.NoError(t, err)
			assert.Equal(t, baseline.Operations, plan.Operations,
				"standard network PE/DNS/subnet/delegation/cross-RG writes are not new BYO authorization gates")
			assert.Equal(t, selection.Network, plan.LegacyNetwork)
			if choice == "reference" || choice == "existing DNS" || choice == "create" {
				assert.Equal(t, vnet+"/subnets/legacy", plan.AgentSubnet.ID)
				assert.Equal(t, choice != "create", plan.AgentSubnet.Supplied)
			}
		})
	}
}
