// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorageSKUStaticPolicy(t *testing.T) {
	exceptions := []string{
		"australiacentral", "australiacentral2", "australiasoutheast", "brazilsoutheast", "canadaeast",
		"francesouth", "germanynorth", "koreasouth", "northcentralus", "norwaywest", "southafricawest",
		"southindia", "swedensouth", "switzerlandwest", "taiwannorth", "taiwannorthwest", "uaecentral",
		"ukwest", "westcentralus", "westindia", "westus",
	}
	require.Len(t, exceptions, 21)
	for _, region := range exceptions {
		t.Run(region, func(t *testing.T) {
			assert.Equal(t, "Standard_LRS", StorageSKU(" "+strings.ToUpper(region)+" "))
		})
	}
	for _, region := range []string{"eastus", "westus2", "westus3", "westeurope", "southeastasia", "future-region"} {
		assert.Equal(t, "Standard_ZRS", StorageSKU(region), "ZRS is the default, not an availability-zone inference")
	}
}

func TestPlannedDeploymentHasOnlyFutureResourceIDs(t *testing.T) {
	f, facts := preparationContractFixture(t, false, false, false)
	data, err := PlannedDeployment(f.plan, facts)
	require.NoError(t, err)
	assert.Equal(t, f.plan.Hosting, data.Hosting)
	assert.Equal(t, f.plan.Workload, data.Workload)
	assert.Equal(t, f.plan.Storage.ID, data.StorageID)
	assert.Equal(t, f.plan.Registry.ID, data.RegistryID)
	assert.Equal(t, StorageSKU(f.plan.Location), data.StorageSKU)
	assert.Empty(t, f.writes)
	assert.Empty(t, f.journal.data)
	for _, mutate := range []func(*Plan){
		func(p *Plan) { p.Workload.ID = p.Hosting.ID },
		func(p *Plan) { p.Hosting.Supplied = true },
		func(p *Plan) { p.Hosting.Name = "other" },
		func(p *Plan) { p.Storage.ID += "?sig=secret" },
		func(p *Plan) { p.Registry.ID = strings.Replace(p.Registry.ID, p.SubscriptionID, "foreign-sub", 1) },
		func(p *Plan) { p.Network = &SubnetPreparation{} },
	} {
		invalid := f.plan
		mutate(&invalid)
		_, err := PlannedDeployment(invalid, facts)
		require.Error(t, err)
	}
}

func TestDeploymentBindingsReadOnlyReuseArbitraryCompatibleKubeletGrant(t *testing.T) {
	for _, abac := range []bool{false, true} {
		t.Run(fmt.Sprint(abac), func(t *testing.T) {
			f, facts := preparationContractFixture(t, true, true, abac)
			registry, err := ParseID(f.plan.Registry.ID, "Microsoft.ContainerRegistry/registries")
			require.NoError(t, err)
			roleID := "/subscriptions/" + registry.SubscriptionID +
				"/providers/Microsoft.Authorization/roleDefinitions/" + registryRoleID(abac)
			id := f.plan.Registry.ID +
				"/providers/Microsoft.Authorization/roleAssignments/00000000-0000-0000-0000-000000000099"
			f.resources[strings.ToLower(id)] = map[string]any{"id": id, "properties": map[string]any{
				"scope": f.plan.Registry.ID, "principalId": facts.KubeletObjectID,
				"roleDefinitionId": roleID, "principalType": "ServicePrincipal", "description": "customer-owned",
			}}
			data, err := ResolveDeploymentBindings(t.Context(), contractCredential{}, f.options(), f.plan, facts)
			require.NoError(t, err)
			assert.Equal(t, id, data.KubeletRoleAssignmentID)
			assert.Equal(t, "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/"+
				"00000000-0000-0000-0000-000000000555", data.FederatedIdentityRoleDefinitionID)
			assert.Equal(t, abac, data.RegistryABAC)
			assert.Empty(t, f.writes)
			assert.Empty(t, f.journal.data, "read-only grant reuse does not claim customer ownership")
		})
	}
}

func TestDeploymentStartJournalsOnlyResourceIntentsBeforeARM(t *testing.T) {
	f := newCleanupARM(t, false, false)
	id := "/subscriptions/" + f.base.plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/first"
	require.Empty(t, f.base.journal.data, "read-only Check does not persist a preparation state")
	require.NoError(t, f.lifecycle.Start(t.Context(), f.base.plan, f.spec, id))
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	require.Len(t, state.Foundry.Attempts, 1)
	assert.Equal(t, id, state.Foundry.Attempts[0].ID)
	assert.Equal(t, f.base.plan.Owner, state.Owner)
	assert.Equal(t, f.spec.Deployment, state.Foundry.Spec.Deployment)
	assert.Empty(t, state.Assignments, "future principal GUIDs are not fabricated")
	assert.Empty(t, state.Foundry.ProjectPrincipalID)
	require.Len(t, state.Resources, 4)
	for step, record := range state.Resources {
		assert.False(t, record.Complete)
		assert.Equal(t, Identity{}, record.Identity, step)
		assert.NotContains(t, f.base.resources, strings.ToLower(record.ID),
			"local intent must precede the only ARM deployment")
	}
	assert.NotContains(t, state.Resources, "R01", "existing resource groups are retained without claiming ownership")
	assert.Empty(t, f.roots)
	assert.Empty(t, f.deletes)
}

func TestLifecycleRejectsUnjournaledDeterministicCollisions(t *testing.T) {
	for _, step := range []string{"R02", "R03", "R04", "R05"} {
		t.Run(step, func(t *testing.T) {
			f := newCleanupARM(t, false, false)
			resource := deploymentResources(f.base.plan)[step]
			// An owner tag alone is not permission to adopt an existing resource.
			f.base.resources[strings.ToLower(resource.ID)] = map[string]any{
				"id": resource.ID, "tags": ownerTags(f.base.plan.Owner),
			}
			_, err := f.lifecycle.Check(t.Context(), f.base.plan, f.spec)
			require.ErrorContains(t, err, "refusing adoption")
			require.Empty(t, f.base.journal.data)
			id := "/subscriptions/" + f.base.plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/first"
			require.Error(t, f.lifecycle.Start(t.Context(), f.base.plan, f.spec, id))
			require.Empty(t, f.base.journal.data)
		})
	}
}

func TestFailedARMGraphRecoveryNeedsNoOutputsOrPredictedGrantUUIDs(t *testing.T) {
	f := newCleanupARM(t, false, true)
	f.deploy(t)
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	state.Assignments = map[string]OwnedAssignment{}
	state.Foundry.ProjectPrincipalID = ""
	for key, record := range state.Resources {
		record.Identity, record.Complete = Identity{}, false
		state.Resources[key] = record
	}
	require.NoError(t, f.base.journal.Save(t.Context(), state))
	for _, root := range f.roots {
		properties := cleanupObject(t, root["properties"])
		properties["provisioningState"] = "Failed"
		delete(properties, "outputs")
	}
	delete(f.base.resources, strings.ToLower(f.base.plan.Workload.ID))
	delete(f.base.resources, strings.ToLower(f.spec.ProjectID))
	for id, resource := range f.base.resources {
		if !strings.Contains(id, "/roleassignments/") {
			continue
		}
		properties := cleanupObject(t, resource["properties"])
		if properties["principalId"] != "00000000-0000-0000-0000-000000000001" &&
			properties["principalId"] != "00000000-0000-0000-0000-000000000003" ||
			properties["scope"] == f.base.plan.Workload.ID {
			delete(f.base.resources, id)
		}
	}
	require.NoError(t, f.lifecycle.Observe(t.Context(), f.base.plan))
	recovered, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "00000000-0000-0000-0000-000000000001", recovered.Resources["R04"].Identity.PrincipalID)
	assert.Empty(t, recovered.Resources["R05"].Identity.PrincipalID)
	assert.Empty(t, recovered.Foundry.ProjectPrincipalID)
	require.Len(t, recovered.Assignments, 3, "recover the two real G01 grants and actual kubelet grant")
	for _, record := range recovered.Assignments {
		assert.Contains(t, f.base.resources, strings.ToLower(record.ID))
		assert.True(t, record.Complete)
	}
	assert.Empty(t, f.base.writes, "recovery is read-only even after a failed/partial graph")
	assert.Empty(t, f.deletes)
}

func TestRegistryEnvironmentOutputRetryPreservesOriginalOwnedLedger(t *testing.T) {
	f := newCleanupARM(t, false, false)
	f.deploy(t)
	before, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	plan := f.base.plan
	plan.Registry.Supplied = true // The normal environment output now resolves as an explicit existing ID.
	data, err := ResolveDeploymentBindings(t.Context(), contractCredential{}, f.options, plan, Facts{
		TenantID: "tenant", KubeletObjectID: f.spec.Deployment.KubeletObjectID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, data.KubeletRoleAssignmentID, "reuse avoids rewriting the prior graph-owned grant")
	spec := f.spec
	spec.Deployment = &data
	_, err = f.lifecycle.Check(t.Context(), plan, spec)
	require.NoError(t, err)
	id := "/subscriptions/" + plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/retry"
	require.NoError(t, f.lifecycle.Start(t.Context(), plan, spec, id))
	after, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	assert.Equal(t, before.Resources["R03"], after.Resources["R03"])
	assert.Equal(t, before.Assignments, after.Assignments)
	assert.Equal(t, before.Foundry.Spec.RegistryID, after.Foundry.Spec.RegistryID)
}

func TestLegacyDependencyLedgerIsNotSilentlyReplaced(t *testing.T) {
	f := newCleanupARM(t, false, false)
	state := PreparationState{
		Version: preparationJournalVersion, Owner: f.base.plan.Owner, SubscriptionID: f.base.plan.SubscriptionID,
		ResourceGroup: f.base.plan.ResourceGroup, Location: f.base.plan.Location,
		Resources: map[string]OwnedResource{"R04": {ID: f.base.plan.Hosting.ID, Complete: true}},
	}
	require.NoError(t, f.base.journal.Save(t.Context(), state))
	before := string(f.base.journal.data)
	_, err := f.lifecycle.Check(t.Context(), f.base.plan, f.spec)
	require.ErrorContains(t, err, "legacy dependency ownership remains")
	id := "/subscriptions/" + f.base.plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/first"
	require.Error(t, f.lifecycle.Start(t.Context(), f.base.plan, f.spec, id))
	assert.Equal(t, before, string(f.base.journal.data))
	_, err = CleanupPlan(state)
	require.NoError(t, err, "the existing exact typed cleanup contract remains usable")
}

func TestFreshLifecycleCollisionCheckDoesNotGETFutureIdentities(t *testing.T) {
	f := newCleanupARM(t, false, false)
	base := f.options
	lists := 0
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		require.NotEqual(t, strings.ToLower(f.base.plan.Hosting.ID), strings.ToLower(request.URL.Path))
		require.NotEqual(t, strings.ToLower(f.base.plan.Workload.ID), strings.ToLower(request.URL.Path))
		if strings.HasSuffix(strings.ToLower(request.URL.Path), "/userassignedidentities") {
			lists++
		}
		return base.Transport.Do(request)
	})
	lifecycle := NewLifecycle(contractCredential{}, options, f.base.journal)
	_, err := lifecycle.Check(t.Context(), f.base.plan, f.spec)
	require.NoError(t, err)
	id := "/subscriptions/" + f.base.plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/first"
	require.NoError(t, lifecycle.Start(t.Context(), f.base.plan, f.spec, id))
	assert.Equal(t, 2, lists, "existing RG identities are listed for collision protection; future identities are not GET")
}

func TestExistingParentResolutionPreservesAllCustomerGroupTags(t *testing.T) {
	f, _ := preparationContractFixture(t, true, true, false)
	groupID := "/subscriptions/" + f.plan.SubscriptionID + "/resourceGroups/" + f.plan.ResourceGroup
	f.resources[strings.ToLower(groupID)]["tags"] = map[string]any{
		"customer": "retained", preparationOwnerTag: "existing-other-owner",
	}
	plan := f.plan
	plan.ResourceGroupSupplied = false
	resolved, err := ResolveExistingParents(t.Context(), plan, contractCredential{}, f.options())
	require.NoError(t, err)
	assert.True(t, resolved.ResourceGroupSupplied)
	require.Contains(t, resolved.ResourceGroupTags, "customer")
	assert.Equal(t, "retained", authString(resolved.ResourceGroupTags["customer"]))
	assert.Equal(t, "existing-other-owner", authString(resolved.ResourceGroupTags[preparationOwnerTag]))
	assert.Empty(t, f.journal.data)
	assert.Empty(t, f.writes)
}

func TestOwnedStorageRetryReferencesExistingSKUWithoutLosingR02Ownership(t *testing.T) {
	for _, sku := range []string{"Standard_LRS", "Standard_ZRS"} {
		t.Run(sku, func(t *testing.T) {
			f := newCleanupARM(t, false, false)
			f.deploy(t)
			require.False(t, f.base.plan.Storage.Supplied, "public Storage input remains omitted")
			require.Equal(t, "Standard_ZRS", StorageSKU(f.base.plan.Location))
			storage := f.base.resources[strings.ToLower(f.base.plan.Storage.ID)]
			storage["sku"] = map[string]any{"name": sku}
			before, err := f.base.journal.Load(t.Context())
			require.NoError(t, err)
			data, err := ResolveDeploymentBindings(t.Context(), contractCredential{}, f.options, f.base.plan, Facts{
				TenantID: "tenant", KubeletObjectID: f.spec.Deployment.KubeletObjectID,
			})
			require.NoError(t, err)
			require.True(t, data.StorageSupplied, "the ARM graph references existing owned Storage, never re-PUTs its SKU")
			assert.False(t, f.base.plan.Storage.Supplied, "reference selection does not turn owned Storage into public input")
			spec := f.spec
			spec.Deployment = &data
			_, err = f.lifecycle.Check(t.Context(), f.base.plan, spec)
			require.NoError(t, err)
			id := "/subscriptions/" + f.base.plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/retry"
			require.NoError(t, f.lifecycle.Start(t.Context(), f.base.plan, spec, id))
			after, err := f.base.journal.Load(t.Context())
			require.NoError(t, err)
			assert.Equal(t, before.Resources["R02"], after.Resources["R02"],
				"the reference branch must not discard the original owned cleanup record")
			assert.True(t, after.Foundry.Spec.Deployment.StorageSupplied)
			assert.Equal(t, sku, cleanupObject(t, storage["sku"])["name"])
			assert.Empty(t, f.base.writes, "read-only resolution and lifecycle observation cannot migrate Storage")
		})
	}
}

func TestOwnedStorageReferenceCannotAdoptAnUnjournaledOwnerTag(t *testing.T) {
	f := newCleanupARM(t, false, false)
	body := storageContractBody(f.base.plan.Storage.ID)
	body["tags"] = ownerTags(f.base.plan.Owner)
	f.base.resources[strings.ToLower(f.base.plan.Storage.ID)] = body
	data, err := ResolveDeploymentBindings(t.Context(), contractCredential{}, f.options, f.base.plan, Facts{
		TenantID: "tenant", KubeletObjectID: f.spec.Deployment.KubeletObjectID,
	})
	require.NoError(t, err)
	require.True(t, data.StorageSupplied)
	spec := f.spec
	spec.Deployment = &data
	_, err = f.lifecycle.Check(t.Context(), f.base.plan, spec)
	require.ErrorContains(t, err, "owned Storage reference requires the exact active journaled resource")
	id := "/subscriptions/" + f.base.plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/first"
	require.Error(t, f.lifecycle.Start(t.Context(), f.base.plan, spec, id))
	assert.Empty(t, f.base.journal.data)
	assert.Empty(t, f.base.writes)
}
