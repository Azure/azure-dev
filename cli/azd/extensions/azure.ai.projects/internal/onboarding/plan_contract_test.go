// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func contractPlanInput() Input {
	return Input{
		ClusterID: "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.ContainerService/managedClusters/aks",
		StorageID: "/subscriptions/storage-sub/resourceGroups/storage-rg/providers/" +
			"Microsoft.Storage/storageAccounts/customer",
		RegistryID: "/subscriptions/acr-sub/resourceGroups/acr-rg/providers/Microsoft.ContainerRegistry/registries/customer",
		AgentSubnetID: "/subscriptions/network-sub/resourceGroups/network-rg/providers/" +
			"Microsoft.Network/virtualNetworks/network/subnets/agents",
		SubscriptionID: "foundry-sub", ResourceGroup: "new-foundry-rg", Location: "eastus",
		Environment: "dev", ProjectPath: "project", FoundryName: "foundry-project",
	}
}

func TestPlanContractExactCreationAndReuseMatrix(t *testing.T) {
	for _, storage := range []bool{false, true} {
		for _, registry := range []bool{false, true} {
			name := "create storage/create registry"
			if storage {
				name = "reuse storage/create registry"
			}
			if registry {
				name += "/reuse registry"
			}
			t.Run(name, func(t *testing.T) {
				input := contractPlanInput()
				if !storage {
					input.StorageID = ""
				}
				if !registry {
					input.RegistryID = ""
				}
				plan, err := BuildPlan(input)
				require.NoError(t, err)
				ancestor := "/subscriptions/foundry-sub"
				want := []Operation{
					{"P02", "Microsoft.ManagedIdentity/userAssignedIdentities/write", ancestor, "", "", ""},
					{"P02", "Microsoft.ManagedIdentity/userAssignedIdentities/read", ancestor, "", "", ""},
					{"P02", "Microsoft.ManagedIdentity/userAssignedIdentities/assign/action", ancestor, "", "", ""},
					{"P03", "Microsoft.ContainerService/managedClusters/write",
						"/subscriptions/aks-sub/resourceGroups/aks-rg", "", "", ""},
					{"P03", "Microsoft.Network/virtualNetworks/write",
						"/subscriptions/network-sub/resourceGroups/network-rg", "", "", ""},
				}
				if storage {
					assert.Equal(t, input.StorageID, plan.Storage.ID)
				} else {
					want = append(want, Operation{
						CheckID: "P04", Action: "Microsoft.Storage/storageAccounts/write", Scope: ancestor,
					})
					assert.Equal(t, ancestor, plan.Storage.Parent)
					assert.Contains(t, plan.Storage.ID, "/resourceGroups/new-foundry-rg/")
				}
				if registry {
					assert.Equal(t, input.RegistryID, plan.Registry.ID)
				} else {
					want = append(want, Operation{
						CheckID: "P05", Action: "Microsoft.ContainerRegistry/registries/write", Scope: ancestor,
					})
					assert.Equal(t, ancestor, plan.Registry.Parent)
					assert.Contains(t, plan.Registry.ID, "/resourceGroups/new-foundry-rg/")
				}
				assert.ElementsMatch(t, want, plan.Operations)
				assert.Equal(t, storage, plan.Storage.Supplied)
				assert.Equal(t, registry, plan.Registry.Supplied)
				assert.Equal(t, registry, plan.RegistryCustomerSupplied)
				assert.Equal(t, input.ClusterID, plan.Cluster.ID)
				assert.True(t, plan.Cluster.Supplied)
				assert.Equal(t, input.AgentSubnetID, plan.AgentSubnet.ID)
				assert.True(t, plan.AgentSubnet.Supplied)
				assert.False(t, plan.Hosting.Supplied)
				assert.False(t, plan.Workload.Supplied)
				assert.NotEqual(t, plan.Hosting.ID, plan.Workload.ID)
				assert.Equal(t, ancestor, plan.Hosting.Parent)
				assert.Equal(t, ancestor, plan.Workload.Parent)
				again, err := BuildPlan(input)
				require.NoError(t, err)
				assert.Equal(t, plan, again, "retries must use the same resource choices and ownership")
			})
		}
	}
}
