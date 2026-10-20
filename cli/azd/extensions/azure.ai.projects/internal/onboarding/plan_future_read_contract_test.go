// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanFutureReadContractRetainsPreparationReadbackPermissions(t *testing.T) {
	const ancestor = "/subscriptions/foundry-sub"
	const resources = ancestor + "/resourceGroups/existing/providers/"
	for _, suppliedStorage := range []bool{false, true} {
		for _, suppliedRegistry := range []bool{false, true} {
			t.Run(fmt.Sprintf("storage=%t/registry=%t", suppliedStorage, suppliedRegistry), func(t *testing.T) {
				input := Input{
					ClusterID: "/subscriptions/aks-sub/resourceGroups/aks/providers/" +
						"Microsoft.ContainerService/managedClusters/customer",
					SubscriptionID: "foundry-sub", ResourceGroup: "new-resources", Location: "eastus",
					Environment: "test", ProjectPath: "project",
				}
				if suppliedStorage {
					input.StorageID = resources + "Microsoft.Storage/storageAccounts/customer"
				}
				if suppliedRegistry {
					input.RegistryID = resources + "Microsoft.ContainerRegistry/registries/customer"
				}
				plan, err := BuildPlan(input)
				require.NoError(t, err)

				for _, required := range []struct {
					action string
					scope  string
				}{
					{action: "Microsoft.Resources/subscriptions/resourceGroups/read", scope: ancestor},
					{
						action: "Microsoft.Storage/storageAccounts/read",
						scope:  plan.Storage.Parent,
					},
					{
						action: "Microsoft.ContainerRegistry/registries/read",
						scope:  plan.Registry.Parent,
					},
				} {
					scope := required.scope
					switch required.action {
					case "Microsoft.Storage/storageAccounts/read":
						if suppliedStorage {
							scope = plan.Storage.ID
						}
					case "Microsoft.ContainerRegistry/registries/read":
						if suppliedRegistry {
							scope = plan.Registry.ID
						}
					}
					assert.True(t, slices.ContainsFunc(plan.Operations, func(op Operation) bool {
						return op.Action == required.action && op.Scope == scope
					}), "preparation requires %s at existing scope %s; do not GET a future resource to prove it",
						required.action, scope)
				}
			})
		}
	}
}
