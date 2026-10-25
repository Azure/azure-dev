// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoveryRequiresGrantIDInExactDeploymentOperationGraph(t *testing.T) {
	f := newCleanupARM(t, false, true)
	f.deploy(t)
	id := f.spec.RegistryID +
		"/providers/Microsoft.Authorization/roleAssignments/00000000-0000-0000-0000-000000000777"
	registry, err := ParseID(f.spec.RegistryID, "Microsoft.ContainerRegistry/registries")
	require.NoError(t, err)
	f.base.resources[strings.ToLower(id)] = map[string]any{"id": id, "properties": map[string]any{
		"scope": f.spec.RegistryID, "principalId": f.spec.Deployment.KubeletObjectID,
		"roleDefinitionId": "/subscriptions/" + registry.SubscriptionID +
			"/providers/Microsoft.Authorization/roleDefinitions/" + registryRoleID(false),
		"principalType": "ServicePrincipal", "description": f.base.plan.Owner,
	}}
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	state.Assignments = map[string]OwnedAssignment{}
	state.Foundry.Attempts[0].OwnershipObserved = false
	require.NoError(t, f.base.journal.Save(t.Context(), state))
	require.NoError(t, f.lifecycle.Observe(t.Context(), f.base.plan))
	state, err = f.base.journal.Load(t.Context())
	require.NoError(t, err)
	require.Len(t, state.Assignments, 6)
	for _, owned := range state.Assignments {
		assert.NotEqual(t, id, owned.ID,
			"matching role, principal and owner description alone cannot adopt an arbitrary preexisting grant")
	}
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
	assert.Contains(t, f.base.resources, strings.ToLower(id))
	assert.NotContains(t, f.deletes, id)
}

func TestUnreadableDeploymentOperationOwnershipRetainsEveryDependency(t *testing.T) {
	f := newCleanupARM(t, false, false)
	f.deploy(t)
	f.operationReadDenied = true
	err := f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil)
	require.ErrorContains(t, err, "ownership recovery")
	assert.Empty(t, f.deletes, "unreadable graph evidence is not permission to delete dependencies")
	resources := []Resource{f.base.plan.Hosting, f.base.plan.Workload, f.base.plan.Storage, f.base.plan.Registry}
	for _, resource := range resources {
		assert.Contains(t, f.base.resources, strings.ToLower(resource.ID))
	}
	f.operationReadDenied = false
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
}

func TestDeploymentGrantRecoveryTraversesExactCrossSubscriptionNestedOperations(t *testing.T) {
	const root = "/subscriptions/target/providers/Microsoft.Resources/deployments/attempt"
	const nested = "/subscriptions/customer/resourceGroups/customer-rg/providers/Microsoft.Resources/deployments/grants"
	const nestedOperations = "/subscriptions/customer/resourcegroups/customer-rg/deployments/grants/operations"
	const grant = "/subscriptions/customer/resourceGroups/customer-rg/providers/Microsoft.ContainerRegistry/registries/acr" +
		"/providers/Microsoft.Authorization/roleAssignments/00000000-0000-0000-0000-000000000999"
	var reads []string
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method)
		reads = append(reads, request.URL.Path)
		var targetID, kind string
		switch request.URL.Path {
		case root + "/operations":
			targetID, kind = nested, "Microsoft.Resources/deployments"
		case nestedOperations:
			targetID, kind = grant, "Microsoft.Authorization/roleAssignments"
		default:
			t.Fatalf("unexpected deployment operation scope %s", request.URL.Path)
		}
		body, err := json.Marshal(map[string]any{"value": []any{map[string]any{"properties": map[string]any{
			"targetResource": map[string]any{"id": targetID, "resourceType": kind},
		}}}})
		require.NoError(t, err)
		return contractARMResponse(request, 200, string(body)), nil
	})
	l := &ownedLifecycle{credential: contractCredential{}, options: onboardingARMOptions(options)}
	targets, err := l.deploymentGrantTargets(t.Context(), Plan{SubscriptionID: "target"}, &FoundryState{
		Attempts: []FoundryAttempt{{ID: root, Terminal: true}},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{strings.ToLower(grant): true}, targets)
	assert.Equal(t, []string{root + "/operations", nestedOperations}, reads)
}
