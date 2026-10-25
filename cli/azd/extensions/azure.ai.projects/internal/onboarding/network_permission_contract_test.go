// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectedNetworkPlansOnlyActualPermissionOperations(t *testing.T) {
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
			ordinary := slices.DeleteFunc(slices.Clone(plan.Operations), func(op Operation) bool {
				return op.CheckID == "P03"
			})
			assert.Equal(t, baseline.Operations, ordinary)
			subnetWrite := false
			for _, op := range plan.Operations {
				assert.NotEqual(t, "Microsoft.ContainerService/managedClusters/write", op.Action)
				assert.NotEqual(t, "Microsoft.Network/virtualNetworks/write", op.Action)
				if op.Action == "Microsoft.Network/virtualNetworks/subnets/write" {
					subnetWrite = true
					assert.Equal(t, vnet, op.Scope)
				}
			}
			assert.Equal(t, choice == "create", subnetWrite)
			if choice == "none" {
				assert.Equal(t, baseline.Operations, plan.Operations)
			} else {
				assert.Contains(t, plan.Operations, Operation{
					CheckID: "P03", Action: "Microsoft.Network/privateEndpoints/write", Scope: "/subscriptions/foundry-sub",
				})
				joinScope := vnet + "/subnets/pe"
				if choice == "create" {
					joinScope = vnet
				}
				assert.Contains(t, plan.Operations, Operation{
					CheckID: "P03", Action: "Microsoft.Network/virtualNetworks/subnets/join/action", Scope: joinScope,
				})
				if choice == "reference" {
					assert.Contains(t, plan.Operations, Operation{
						CheckID: "P03", Action: "Microsoft.Network/virtualNetworks/subnets/join/action",
						Scope: vnet + "/subnets/legacy",
					})
				}
			}
			assert.Equal(t, selection.Network, plan.LegacyNetwork)
			if choice == "reference" || choice == "existing DNS" || choice == "create" {
				assert.Equal(t, vnet+"/subnets/legacy", plan.AgentSubnet.ID)
				assert.Equal(t, choice != "create", plan.AgentSubnet.Supplied)
			}
		})
	}
}
