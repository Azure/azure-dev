// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestP09ComparesFinalGroupIDsAndDeduplicatesScopes(t *testing.T) {
	for _, choice := range []string{"same group", "same name other subscription", "other group", "shared external group"} {
		for _, groupExists := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", choice, groupExists), func(t *testing.T) {
				input := contractPlanInput()
				target := "/subscriptions/" + input.SubscriptionID + "/resourceGroups/" + input.ResourceGroup
				scope := target
				switch choice {
				case "same name other subscription":
					scope = strings.Replace(target, input.SubscriptionID, "other-sub", 1)
				case "other group", "shared external group":
					scope = "/subscriptions/" + input.SubscriptionID + "/resourceGroups/elsewhere"
				}
				input.ClusterID = scope + "/providers/Microsoft.ContainerService/managedClusters/aks"
				input.StorageID = target + "/providers/Microsoft.Storage/storageAccounts/storage"
				input.RegistryID = target + "/providers/Microsoft.ContainerRegistry/registries/registry"
				if choice == "shared external group" {
					input.StorageID = strings.Replace(input.StorageID, target, strings.ToUpper(scope), 1)
					input.RegistryID = strings.Replace(input.RegistryID, target, scope, 1)
				}
				plan, err := BuildPlan(input)
				require.NoError(t, err)
				options := contractARMOptions(func(r *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, r.Method)
					if !groupExists {
						return contractARMResponse(r, 404, `{"error":{"code":"ResourceGroupNotFound"}}`), nil
					}
					return contractARMResponse(r, 200, `{"location":"eastus"}`), nil
				})
				plan, err = ResolveExistingParents(t.Context(), plan, contractCredential{}, options)
				require.NoError(t, err)
				plan, err = WithDeploymentOperations(plan, DeploymentSelection{})
				require.NoError(t, err)
				var actual []Operation
				for _, op := range plan.Operations {
					if op.CheckID == "P09" {
						actual = append(actual, op)
						assert.True(t, strings.EqualFold(scope, op.Scope))
					}
				}
				if choice == "same group" {
					assert.Empty(t, actual, "future target RG must not be confused with its subscription ancestor")
				} else {
					require.Len(t, actual, 3, "shared external scope is checked only once per action")
					for _, action := range []string{"write", "read", "operations/read"} {
						assert.Contains(t, actual, Operation{
							CheckID: "P09", Action: "Microsoft.Resources/deployments/" + action, Scope: scope,
						})
					}
				}
				again, err := WithDeploymentOperations(plan, DeploymentSelection{})
				require.NoError(t, err)
				assert.Equal(t, plan.Operations, again.Operations)
			})
		}
	}
}

func p09ContractAuthorizer(t *testing.T, plan Plan, facts Facts, excludeScope, excludeAction string) (Report, error) {
	t.Helper()
	var currentScope string
	options := contractARMOptions(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, r.Method)
		var body string
		switch {
		case strings.HasSuffix(r.URL.Path, "/roleAssignments"):
			currentScope = strings.TrimSuffix(r.URL.Path, "/providers/Microsoft.Authorization/roleAssignments")
			body = fmt.Sprintf(`{"value":[{"properties":{"scope":%q,"principalId":"operator","principalType":"User",
				"roleDefinitionId":"/providers/Microsoft.Authorization/roleDefinitions/custom"}}]}`, currentScope)
		case strings.Contains(r.URL.Path, "/roleDefinitions/"):
			excluded := []string{
				"Microsoft.CognitiveServices/accounts/read", "Microsoft.CognitiveServices/accounts/write",
				"Microsoft.CognitiveServices/accounts/projects/*", "Microsoft.CognitiveServices/accounts/deployments/write",
				"Microsoft.Network/privateEndpoints/*", "Microsoft.Network/privateDnsZones/*",
				"Microsoft.Network/virtualNetworks/subnets/*", "Microsoft.CognitiveServices/accounts/managedNetworks/write",
			}
			if strings.EqualFold(currentScope, excludeScope) {
				excluded = append(excluded, excludeAction)
			}
			raw, err := json.Marshal(excluded)
			require.NoError(t, err)
			body = fmt.Sprintf(`{"properties":{"permissions":[{"actions":["*"],"notActions":%s}]}}`, raw)
		default:
			t.Fatalf("unexpected authorization request: %s", r.URL.Path)
		}
		return contractARMResponse(r, 200, body), nil
	})
	return NewAuthorizer(removedChecksCredential{}, options).Check(t.Context(), plan, facts)
}

func TestP09F01OnlyForNewRegistryBranches(t *testing.T) {
	for _, choice := range []string{"owned RBAC", "owned ABAC", "customer same group", "customer other group",
		"internal reference", "extra scope"} {
		t.Run(choice, func(t *testing.T) {
			input := contractPlanInput()
			input.RegistryID = ""
			if strings.HasPrefix(choice, "customer") {
				group := input.ResourceGroup
				if choice == "customer other group" {
					group = "elsewhere"
				}
				input.RegistryID = "/subscriptions/" + input.SubscriptionID + "/resourceGroups/" + group +
					"/providers/Microsoft.ContainerRegistry/registries/customer"
			}
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			if choice == "internal reference" {
				plan.Registry.Supplied = true
			}
			if choice == "extra scope" {
				plan.Registry.ID = strings.Replace(plan.Registry.ID, input.ResourceGroup, "elsewhere", 1)
				plan.Registry.Parent = "/subscriptions/foundry-sub/resourceGroups/elsewhere"
				plan.Registry.Supplied = true
			}
			facts := Facts{KubeletObjectID: "kubelet", RegistryABAC: choice == "owned ABAC"}
			report, err := p09ContractAuthorizer(t, plan, facts, "/subscriptions/"+plan.SubscriptionID,
				"Microsoft.Resources/deployments/*")
			require.NoError(t, err, "old Foundry and standard network permissions are not repeated")
			var projectGrants []Check
			for _, check := range report {
				if check.ID == "P09" {
					projectGrants = append(projectGrants, check)
					assert.Equal(t, roleAssignmentWrite, check.Expected)
					assert.NotContains(t, check.Expected, "kubelet", "F01 must not substitute the G04 principal")
				}
			}
			wanted := choice != "owned RBAC" && choice != "internal reference"
			if wanted {
				require.Len(t, projectGrants, 1)
			} else {
				assert.Empty(t, projectGrants)
			}
			assert.True(t, slices.ContainsFunc(report, func(c Check) bool { return c.ID == "P07" && c.Status == Pass }))
			if wanted {
				scope := plan.Registry.ID
				if !plan.Registry.Supplied {
					scope = plan.Registry.Parent
				}
				failed, err := p09ContractAuthorizer(t, plan, facts, scope, roleAssignmentWrite)
				require.Error(t, err)
				assert.True(t, slices.ContainsFunc(failed, func(c Check) bool {
					return c.ID == "P09" && c.Status == Fail && c.Scope == scope
				}), "missing new-branch grant authority is still mandatory")
			}
		})
	}
}

func TestP09OwnedRegistryOutputRetainsOriginalSelectionOnRetry(t *testing.T) {
	f := newCleanupARM(t, false, false)
	f.deploy(t)
	input := Input{
		ClusterID: f.base.plan.Cluster.ID, RegistryID: f.base.plan.Registry.ID,
		SubscriptionID: f.base.plan.SubscriptionID, ResourceGroup: f.base.plan.ResourceGroup,
		Location: f.base.plan.Location, Environment: f.base.plan.Environment, ProjectPath: "retry",
		FoundryName: "foundry-project",
	}
	plan, err := BuildPlan(input)
	require.NoError(t, err)
	plan.Owner = f.base.plan.Owner
	require.True(t, plan.RegistryCustomerSupplied)
	plan, err = ResolveRegistrySelection(t.Context(), plan, f.base.journal)
	require.NoError(t, err)
	assert.False(t, plan.RegistryCustomerSupplied)
	assert.True(t, plan.Registry.Supplied, "ARM reference behavior and ownership ledger remain unchanged")
	report, err := p09ContractAuthorizer(t, plan, Facts{KubeletObjectID: "kubelet"}, "", "")
	require.NoError(t, err)
	assert.False(t, slices.ContainsFunc(report, func(c Check) bool { return c.ID == "P09" }))

	input.RegistryID += "different"
	different, err := BuildPlan(input)
	require.NoError(t, err)
	different.Owner = plan.Owner
	different, err = ResolveRegistrySelection(t.Context(), different, f.base.journal)
	require.NoError(t, err)
	assert.True(t, different.RegistryCustomerSupplied, "different customer registry must retain the new grant check")
}

func TestP09OriginalRegistrySelectionReadFailuresRemainExplicit(t *testing.T) {
	plan, err := BuildPlan(contractPlanInput())
	require.NoError(t, err)
	_, err = ResolveRegistrySelection(t.Context(), plan, failedRegistryJournal{})
	require.ErrorContains(t, err, "I03")
	require.ErrorContains(t, err, "journal unavailable")
}

type failedRegistryJournal struct{}

func (failedRegistryJournal) Load(context.Context) (PreparationState, error) {
	return PreparationState{}, fmt.Errorf("journal unavailable")
}

func (failedRegistryJournal) Save(context.Context, PreparationState) error {
	return fmt.Errorf("unexpected journal write")
}
