// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cleanupObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "expected fixture object, got %T", value)
	return object
}

type cleanupARM struct {
	t                   *testing.T
	base                *preparationARM
	options             *arm.ClientOptions
	lifecycle           Lifecycle
	spec                FoundrySpec
	roots               map[string]map[string]any
	deletes             []string
	accountStatus       int
	failDelete          string
	keepAccount         bool
	ficRemains          bool
	cancel              context.CancelFunc
	cancelOnDelete      int
	asyncAccount        bool
	purged              bool
	softDeleted         bool
	rootReadMissing     bool
	operationTargets    []string
	operationReadDenied bool
	failJournalOnDelete string
	denyPurge           bool
	deploymentState     string
	afterMaterialize    func()
	operationStates     map[string]string
}

func newCleanupARM(t *testing.T, suppliedStorage, suppliedRegistry bool, suppliedIdentities ...bool) *cleanupARM {
	t.Helper()
	base, facts := preparationContractFixture(t, suppliedStorage, suppliedRegistry, false, suppliedIdentities...)
	data, err := ResolveDeploymentBindings(t.Context(), contractCredential{}, base.options(), base.plan, facts)
	require.NoError(t, err)
	template := map[string]any{
		"$schema":   "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		"resources": []any{},
	}
	digest, err := TemplateDigest(template)
	require.NoError(t, err)
	account := PlannedFoundryAccount(base.plan)
	f := &cleanupARM{
		t: t, base: base, roots: map[string]map[string]any{}, accountStatus: http.StatusOK,
		spec: FoundrySpec{AccountID: account.ID, ProjectID: account.ID + "/projects/project",
			RegistryID: base.plan.Registry.ID, Location: base.plan.Location,
			ModelNames: []string{"owned-model"}, TemplateHash: "offline-standard-template-hash",
			TemplateDigest: digest, Deployment: &data},
	}
	baseOptions := base.options()
	f.options = contractARMOptions(func(request *http.Request) (*http.Response, error) {
		path := strings.ToLower(request.URL.Path)
		respond := func(status int, body any) (*http.Response, error) {
			var data []byte
			if body != nil {
				var err error
				data, err = json.Marshal(body)
				require.NoError(t, err)
			}
			return contractARMResponse(request, status, string(data)), nil
		}
		notFound := func() (*http.Response, error) {
			return respond(http.StatusNotFound, map[string]any{"error": map[string]any{"code": "ResourceNotFound"}})
		}
		if strings.Contains(path, "/providers/microsoft.resources/deployments/") {
			if strings.HasSuffix(path, "/exporttemplate") {
				require.Equal(t, http.MethodPost, request.Method, "template export is a read-only ARM operation")
				root, exists := f.roots[strings.TrimSuffix(path, "/exporttemplate")]
				if !exists {
					return notFound()
				}
				return respond(200, map[string]any{"template": cleanupObject(t, root["properties"])["template"]})
			}
			if strings.HasSuffix(path, "/operations") {
				require.Equal(t, http.MethodGet, request.Method)
				if f.operationReadDenied {
					return respond(403, map[string]any{"error": map[string]any{"code": "AuthorizationFailed"}})
				}
				var operations []any
				for _, id := range f.operationTargets {
					operationState := f.operationStates[strings.ToLower(id)]
					if operationState == "" {
						operationState = "Succeeded"
					}
					operations = append(operations, map[string]any{"properties": map[string]any{
						"provisioningState": operationState,
						"targetResource": map[string]any{
							"id": id, "resourceType": "Microsoft.Authorization/roleAssignments",
						},
					}})
				}
				return respond(200, map[string]any{"value": operations})
			}
			switch request.Method {
			case http.MethodGet:
				if root, exists := f.roots[path]; exists && !f.rootReadMissing {
					return respond(200, root)
				}
				return notFound()
			case http.MethodPut:
				state, err := base.journal.Load(request.Context())
				require.NoError(t, err)
				require.NotNil(t, state.Foundry)
				require.Equal(t, request.URL.Path, state.Foundry.Attempts[len(state.Foundry.Attempts)-1].ID,
					"actual deployment ID must be durable before its PUT")
				var body map[string]any
				require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
				body["id"] = request.URL.Path
				properties := cleanupObject(t, body["properties"])
				properties["provisioningState"], properties["correlationId"] = "Succeeded", "correlation-"+path
				if f.deploymentState != "" {
					properties["provisioningState"] = f.deploymentState
				}
				properties["templateHash"] = "6828760392490666086"
				properties["template"] = template
				f.roots[path] = body
				base.materializeGraph(f.spec)
				for id, resource := range base.resources {
					if strings.Contains(id, "/roleassignments/") &&
						cleanupObject(t, resource["properties"])["description"] == base.plan.Owner {
						f.operationTargets = append(f.operationTargets, id)
					}
				}
				base.resources[strings.ToLower(f.spec.AccountID)] = map[string]any{
					"id": f.spec.AccountID, "location": f.spec.Location, "tags": ownerTags(base.plan.Owner),
					"properties": map[string]any{"provisioningState": "Succeeded"},
				}
				base.resources[strings.ToLower(f.spec.ProjectID)] = map[string]any{
					"id": f.spec.ProjectID, "tags": ownerTags(base.plan.Owner), "location": f.spec.Location,
					"properties": map[string]any{"provisioningState": "Succeeded"},
					"identity": map[string]any{"type": "SystemAssigned",
						"tenantId":    data.TenantID,
						"principalId": "00000000-0000-0000-0000-000000000004"},
				}
				for _, name := range f.spec.ModelNames {
					id := f.spec.AccountID + "/deployments/" + name
					base.resources[strings.ToLower(id)] = map[string]any{"id": id, "name": name}
				}
				if f.afterMaterialize != nil {
					f.afterMaterialize()
				}
				return respond(200, body)
			default:
				t.Fatalf("deployment records must never be deleted during owned cleanup: %s", request.Method)
			}
		}
		if strings.Contains(path, "/deletedaccounts/") {
			require.Equal(t, "2025-06-01", request.URL.Query().Get("api-version"))
			if request.Method == http.MethodDelete {
				if f.denyPurge {
					return respond(403, map[string]any{"error": map[string]any{"code": "AuthorizationFailed"}})
				}
				require.True(t, f.softDeleted, "purge only after deletion of the exact owned account")
				f.purged, f.softDeleted = true, false
				f.deletes = append(f.deletes, request.URL.Path)
				return respond(204, nil)
			}
			if f.softDeleted {
				return respond(200, map[string]any{
					"id": f.spec.AccountID, "location": f.spec.Location, "tags": ownerTags(base.plan.Owner),
				})
			}
			return notFound()
		}
		if strings.Contains(path, "/operations/delete-owned-account") {
			require.Equal(t, http.MethodGet, request.Method)
			return respond(200, map[string]any{"status": "Succeeded"})
		}
		if request.Method == http.MethodGet {
			if strings.HasSuffix(path, "/federatedidentitycredentials") {
				values := []any{}
				if f.ficRemains {
					values = append(values, map[string]any{"name": "service-owned"})
				}
				return respond(200, map[string]any{"value": values})
			}
			if path == strings.ToLower(f.spec.AccountID)+"/projects" ||
				path == strings.ToLower(f.spec.AccountID)+"/deployments" {
				values := []any{}
				for key, resource := range base.resources {
					if strings.HasPrefix(key, path+"/") && !strings.Contains(strings.TrimPrefix(key, path+"/"), "/") {
						values = append(values, resource)
					}
				}
				return respond(200, map[string]any{"value": values})
			}
		}
		if request.Method == http.MethodDelete {
			require.NotContains(t, path, "/federatedidentitycredentials/",
				"service-owned FIC teardown must never be replaced by azd")
			require.False(t, strings.HasSuffix(path, "/"+strings.ToLower(base.plan.ResourceGroup)),
				"resource group deletion must never occur")
			require.NotEqual(t, strings.ToLower(base.plan.Cluster.ID), path)
			if base.plan.Storage.Supplied {
				require.NotEqual(t, strings.ToLower(base.plan.Storage.ID), path)
			}
			if base.plan.Registry.Supplied {
				require.NotEqual(t, strings.ToLower(base.plan.Registry.ID), path)
			}
			if path == strings.ToLower(f.failDelete) {
				return respond(403, map[string]any{"error": map[string]any{"code": "AuthorizationFailed"}})
			}
			f.deletes = append(f.deletes, request.URL.Path)
			if strings.EqualFold(request.URL.Path, f.failJournalOnDelete) {
				base.journal.fail = true
			}
			if path == strings.ToLower(f.spec.AccountID) {
				require.Equal(t, "2025-06-01", request.URL.Query().Get("api-version"))
				for id := range base.resources {
					require.False(t, strings.HasPrefix(id, path+"/deployments/"), "models must be deleted before account")
				}
				if !f.keepAccount {
					delete(base.resources, path)
					delete(base.resources, strings.ToLower(f.spec.ProjectID))
					for child := range base.resources {
						if strings.HasPrefix(child, path+"/") {
							delete(base.resources, child)
						}
					}
					f.softDeleted = true
				}
				if f.asyncAccount {
					response, err := respond(202, nil)
					response.Header.Set("Azure-AsyncOperation",
						"https://management.azure.com/subscriptions/sub/providers/Microsoft.CognitiveServices/operations/delete-owned-account")
					response.Header.Set("Retry-After", "0")
					return response, err
				}
				if f.cancel != nil && len(f.deletes) == f.cancelOnDelete {
					f.cancel()
				}
				return respond(f.accountStatus, nil)
			}
			delete(base.resources, path)
			if f.cancel != nil && len(f.deletes) == f.cancelOnDelete {
				f.cancel()
			}
			return respond(204, nil)
		}
		return baseOptions.Transport.Do(request)
	})
	f.lifecycle = NewLifecycle(contractCredential{}, f.options, base.journal)
	report, err := f.lifecycle.Check(t.Context(), base.plan, f.spec)
	require.NoError(t, err)
	require.NoError(t, report.Err())
	return f
}

func (f *cleanupARM) deploy(t *testing.T) {
	t.Helper()
	require.NoError(t, f.submit(t))
	require.NoError(t, f.lifecycle.Observe(t.Context(), f.base.plan))
}

func (f *cleanupARM) submit(t *testing.T) error {
	t.Helper()
	client, err := armresources.NewDeploymentsClient(f.base.plan.SubscriptionID, contractCredential{}, f.options)
	require.NoError(t, err)
	for index := range 1 {
		name := fmt.Sprintf("owned-attempt-%d", len(f.roots)+index)
		id := "/subscriptions/" + f.base.plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/" + name
		require.NoError(t, f.lifecycle.Start(t.Context(), f.base.plan, f.spec, id))
		poller, err := client.BeginCreateOrUpdateAtSubscriptionScope(t.Context(), name, armresources.Deployment{
			Location: new(f.spec.Location), Tags: ownerTags(f.base.plan.Owner),
			Properties: &armresources.DeploymentProperties{
				Mode: new(armresources.DeploymentModeIncremental), Template: map[string]any{},
				Parameters: map[string]any{
					"byoOnboarding": map[string]any{"value": map[string]any{
						"owner": f.base.plan.Owner, "accountName": PlannedFoundryAccount(f.base.plan).Name,
						"clusterResourceId":        f.base.plan.Cluster.ID,
						"storageAccountResourceId": f.base.plan.Storage.ID, "registryResourceId": f.base.plan.Registry.ID,
						"hostingIdentityName": f.base.plan.Hosting.Name, "workloadIdentityName": f.base.plan.Workload.Name,
						"hostingIdentityResourceId":  f.base.plan.Hosting.ID,
						"workloadIdentityResourceId": f.base.plan.Workload.ID,
						"hostingIdentitySupplied":    f.base.plan.Hosting.Supplied,
						"workloadIdentitySupplied":   f.base.plan.Workload.Supplied,
						"hostingPrincipalId":         f.spec.Deployment.HostingIdentity.PrincipalID,
						"workloadPrincipalId":        f.spec.Deployment.WorkloadIdentity.PrincipalID,
						"createClusterGrants":        createsGrant(f.base.plan, "G01"),
						"createFederationGrant":      createsGrant(f.base.plan, "G02"),
						"createStorageGrant":         createsGrant(f.base.plan, "G03"),
					}},
					"resourceGroupName":  map[string]any{"value": f.base.plan.ResourceGroup},
					"location":           map[string]any{"value": f.spec.Location},
					"foundryProjectName": map[string]any{"value": "project"},
				},
			},
		}, nil)
		if err != nil {
			return err
		}
		_, err = poller.PollUntilDone(t.Context(), nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func TestOwnedCleanupSDKLifecyclePreservesSuppliedAndLaterAddedResources(t *testing.T) {
	for _, storage := range []bool{false, true} {
		for _, registry := range []bool{false, true} {
			t.Run(fmt.Sprintf("storage=%t/registry=%t", storage, registry), func(t *testing.T) {
				f := newCleanupARM(t, storage, registry)
				f.deploy(t)
				customer := "/subscriptions/" + f.base.plan.SubscriptionID + "/resourceGroups/" +
					f.base.plan.ResourceGroup + "/providers/Microsoft.Storage/storageAccounts/later-added"
				f.base.resources[strings.ToLower(customer)] = map[string]any{"id": customer}
				state, err := f.base.journal.Load(t.Context())
				require.NoError(t, err)
				assert.Len(t, state.Assignments, 6, "five prerequisite grants and the actual project-MI grant")
				require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, func(string) {}))
				assert.Contains(t, f.base.resources, strings.ToLower(customer))
				assert.Contains(t, f.base.resources, strings.ToLower("/subscriptions/"+f.base.plan.SubscriptionID+
					"/resourceGroups/"+f.base.plan.ResourceGroup))
				if storage {
					assert.Contains(t, f.base.resources, strings.ToLower(f.base.plan.Storage.ID))
				}
				if registry {
					assert.Contains(t, f.base.resources, strings.ToLower(f.base.plan.Registry.ID))
				}
				state, err = f.base.journal.Load(t.Context())
				require.NoError(t, err)
				assert.True(t, state.CleanupComplete)
				assert.True(t, state.Foundry.AccountDeleted)
				assert.False(t, state.Foundry.Purged, "normal delete must not silently purge")
				for step, record := range state.Resources {
					if step != "R01" {
						assert.True(t, record.Deleted, step)
					}
				}
				for _, grant := range state.Assignments {
					assert.True(t, grant.Deleted)
					assert.NotContains(t, f.base.resources, strings.ToLower(grant.ID))
				}
				before := len(f.deletes)
				require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
				assert.Len(t, f.deletes, before, "completed cleanup is idempotent")
			})
		}
	}
}

func TestOwnedCleanupSDKStopsAndResumesAfterPermissionFailure(t *testing.T) {
	f := newCleanupARM(t, false, true)
	f.deploy(t)
	f.failDelete = f.base.plan.Storage.ID
	err := f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil)
	require.ErrorContains(t, err, "AuthorizationFailed")
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	assert.True(t, state.Foundry.AccountDeleted)
	assert.False(t, state.CleanupComplete)
	assert.False(t, state.Resources["R02"].Deleted)
	assert.False(t, state.Resources["R04"].Deleted)
	for _, record := range state.Assignments {
		assert.True(t, record.Deleted)
	}
	prior := len(f.deletes)
	f.failDelete = ""
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
	assert.NotContains(t, f.deletes[prior:], f.spec.AccountID, "resume must not delete the account twice")
	state, err = f.base.journal.Load(t.Context())
	require.NoError(t, err)
	assert.True(t, state.CleanupComplete)
}

func TestOwnedCleanupSDKRejectsAmbiguousOrChangedOwnership(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*cleanupARM)
		want   string
	}{
		{"account owner changed", func(f *cleanupARM) {
			f.base.resources[strings.ToLower(f.spec.AccountID)]["tags"] = ownerTags("another-owner")
		}, "ownership"},
		{"unknown deployment", func(f *cleanupARM) {
			f.rootReadMissing = true
			state, err := f.base.journal.Load(f.t.Context())
			require.NoError(f.t, err)
			state.Foundry.Attempts[0].Terminal = false
			require.NoError(f.t, f.base.journal.Save(f.t.Context(), state))
		}, "unknown"},
		{"active deployment", func(f *cleanupARM) {
			for _, root := range f.roots {
				cleanupObject(f.t, root["properties"])["provisioningState"] = "Running"
			}
		}, "active"},
		{"changed actual template", func(f *cleanupARM) {
			delete(f.base.resources, strings.ToLower(f.spec.AccountID))
			for _, root := range f.roots {
				cleanupObject(f.t, root["properties"])["templateHash"] = "different-template"
			}
		}, "actual template binding"},
		{"partial failed unknown template", func(f *cleanupARM) {
			delete(f.base.resources, strings.ToLower(f.spec.AccountID))
			for _, root := range f.roots {
				properties := cleanupObject(f.t, root["properties"])
				properties["provisioningState"] = "Failed"
				delete(properties, "templateHash")
			}
		}, "actual template binding"},
		{"later added project", func(f *cleanupARM) {
			id := f.spec.AccountID + "/projects/customer-added"
			f.base.resources[strings.ToLower(id)] = map[string]any{"id": id}
		}, "untracked"},
		{"later added model", func(f *cleanupARM) {
			id := f.spec.AccountID + "/deployments/customer-added"
			f.base.resources[strings.ToLower(id)] = map[string]any{"id": id}
		}, "untracked"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			test.mutate(f)
			require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil), test.want)
			assert.Empty(t, f.deletes)
		})
	}
}

func TestOwnedCleanupSDKRequiresAccountAbsenceAndServiceFICTeardown(t *testing.T) {
	for _, fic := range []bool{false, true} {
		t.Run(fmt.Sprintf("fic=%t", fic), func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			f.keepAccount, f.ficRemains = !fic, fic
			err := f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil)
			require.Error(t, err)
			if fic {
				assert.ErrorContains(t, err, "federated credentials")
			} else {
				assert.ErrorContains(t, err, "not yet confirmed")
			}
			for _, path := range f.deletes {
				assert.True(t, path == f.spec.AccountID || strings.HasPrefix(path, f.spec.AccountID+"/deployments/"),
					"no dependency/grant mutation until account and service identity teardown are confirmed")
			}
		})
	}
}

func TestOwnedCleanupSDKCancellationStopsFurtherMutationsAndCanResume(t *testing.T) {
	f := newCleanupARM(t, false, false)
	f.deploy(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.cancel, f.cancelOnDelete = cancel, 1
	require.ErrorIs(t, f.lifecycle.Cleanup(ctx, f.base.plan, false, nil), context.Canceled)
	assert.Len(t, f.deletes, 1)
	f.cancel = nil
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
}

func TestOwnedCleanupSDKExplicitPurgeIsSeparateAndResumable(t *testing.T) {
	f := newCleanupARM(t, true, true)
	f.deploy(t)
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
	assert.False(t, f.purged)
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, true, nil))
	assert.True(t, f.purged)
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	assert.True(t, state.Foundry.Purged)
}

func TestOwnedCleanupSDKAccountDeleteSupportedResponses(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		status int
		async  bool
	}{
		{"empty-200", http.StatusOK, false},
		{"empty-204", http.StatusNoContent, false},
		{"async-202", http.StatusAccepted, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			f.accountStatus, f.asyncAccount = scenario.status, scenario.async
			require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			state, err := f.base.journal.Load(t.Context())
			require.NoError(t, err)
			require.True(t, state.CleanupComplete)
			require.True(t, state.Foundry.AccountDeleted)
			require.False(t, f.purged)
		})
	}
}

func TestOwnedCleanupSDKLiveRegionAndProjectOwnerAreBound(t *testing.T) {
	for _, project := range []bool{false, true} {
		t.Run(fmt.Sprintf("project=%t", project), func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			if project {
				f.base.resources[strings.ToLower(f.spec.ProjectID)]["tags"] = ownerTags("another-owner")
			} else {
				f.base.resources[strings.ToLower(f.spec.AccountID)]["location"] = "westus"
				_, err := f.lifecycle.Check(t.Context(), f.base.plan, f.spec)
				require.Error(t, err)
			}
			if project {
				require.Error(t, f.lifecycle.Observe(t.Context(), f.base.plan))
			}
			require.Error(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			require.Empty(t, f.deletes)
		})
	}
}

func TestOwnedCleanupSDKDoesNotDeleteRecreatedModel(t *testing.T) {
	f := newCleanupARM(t, true, true)
	f.deploy(t)
	f.failDelete = f.spec.AccountID
	require.Error(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	require.True(t, state.Foundry.ModelsDeleted["owned-model"])
	model := f.spec.AccountID + "/deployments/owned-model"
	f.base.resources[strings.ToLower(model)] = map[string]any{"id": model}
	f.failDelete = ""
	before := len(f.deletes)
	require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil), "recreated")
	require.Len(t, f.deletes, before)
}

func TestOwnedCleanupSDKSubnetSafety(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		field   string
		value   any
		blocked bool
	}{
		{name: "exact-owned-subnet"},
		{name: "already-absent"},
		{"changed-CIDR", "addressPrefix", "10.99.0.32/27", true},
		{"in-use", "ipConfigurations", []any{map[string]any{"id": "/in-use"}}, true},
		{"route-attached", "routeTable", map[string]any{"id": "/customer-route"}, true},
		{"IPAM-pool", "ipamPoolPrefixAllocations", []any{map[string]any{"numberOfIpAddresses": "32"}}, true},
		{"shared-subnet", "sharingScope", "Tenant", true},
		{"changed-delegation", "delegations", []any{}, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			state, err := f.base.journal.Load(t.Context())
			require.NoError(t, err)
			vnet := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/virtualNetworks/node"
			id := vnet + "/subnets/owned"
			state.Network = &SubnetPreparation{
				VNetID: vnet, SubnetID: id, AddressPrefix: "10.99.0.0/27", Location: f.spec.Location,
			}
			state.Resources["R06Subnet"] = OwnedResource{ID: id, Complete: true}
			require.NoError(t, f.base.journal.Save(t.Context(), state))
			properties := map[string]any{
				"addressPrefix": "10.99.0.0/27", "provisioningState": "Succeeded",
				"delegations": []any{map[string]any{
					"properties": map[string]any{"serviceName": "Microsoft.App/environments"},
				}},
			}
			if scenario.field != "" {
				properties[scenario.field] = scenario.value
			}
			if scenario.name != "already-absent" {
				f.base.resources[strings.ToLower(id)] = map[string]any{"id": id, "properties": properties}
			}
			err = f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil)
			if scenario.blocked {
				require.ErrorContains(t, err, "subnet topology changed")
				assert.NotContains(t, f.deletes, id)
			} else {
				require.NoError(t, err)
			}
			state, err = f.base.journal.Load(t.Context())
			require.NoError(t, err)
			assert.Equal(t, !scenario.blocked, state.Resources["R06Subnet"].Deleted)
			assert.Equal(t, !scenario.blocked, state.CleanupComplete)
		})
	}
}

func TestOwnedCleanupSDKAbsentResourcesAndDurableTombstones(t *testing.T) {
	f := newCleanupARM(t, false, false)
	f.deploy(t)
	for id := range f.base.resources {
		if !strings.Contains(id, "/resourcegroups/") || strings.Contains(id, "/providers/") {
			delete(f.base.resources, id)
		}
	}
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
	require.Empty(t, f.deletes)
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	require.True(t, state.CleanupComplete)
	for step, resource := range state.Resources {
		if step != "R01" {
			require.True(t, resource.Deleted)
			f.base.resources[strings.ToLower(resource.ID)] = map[string]any{"id": resource.ID}
		}
	}
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
	require.Empty(t, f.deletes, "tombstones must protect resources later recreated at the same IDs")
}

func TestOwnedCleanupSDKJournalFailurePreventsAnyDelete(t *testing.T) {
	f := newCleanupARM(t, true, true)
	f.deploy(t)
	f.base.journal.fail = true
	require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil), "persist cleanup intent")
	require.Empty(t, f.deletes)
	f.base.journal.fail = false
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
}

func TestOwnedCleanupSDKPreservesUntrackedCompatibleProjectGrant(t *testing.T) {
	f := newCleanupARM(t, true, true)
	id := f.spec.RegistryID + "/providers/Microsoft.Authorization/roleAssignments/00000000-0000-0000-0000-000000000088"
	registry, err := ParseID(f.spec.RegistryID, "Microsoft.ContainerRegistry/registries")
	require.NoError(t, err)
	f.base.resources[strings.ToLower(id)] = map[string]any{
		"id": id, "properties": map[string]any{
			"scope": f.spec.RegistryID, "principalId": "00000000-0000-0000-0000-000000000004",
			"principalType": "ServicePrincipal", "description": "customer-owned",
			"roleDefinitionId": "/subscriptions/" + registry.SubscriptionID +
				"/providers/Microsoft.Authorization/roleDefinitions/" + registryRoleID(false),
		},
	}
	f.deploy(t)
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	for _, assignment := range state.Assignments {
		assert.NotEqual(t, id, assignment.ID, "reused grants must not be adopted into the ownership ledger")
	}
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
	assert.NotContains(t, f.deletes, id)
	require.Contains(t, f.base.resources, strings.ToLower(id))
	properties := cleanupObject(t, f.base.resources[strings.ToLower(id)]["properties"])
	assert.Equal(t, "customer-owned", properties["description"])
}

func TestOwnedCleanupSDKJournalFailureAfterDeleteResumesFromAbsence(t *testing.T) {
	f := newCleanupARM(t, false, true)
	f.deploy(t)
	f.failJournalOnDelete = f.base.plan.Storage.ID
	require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil), "journal unavailable")
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	require.False(t, state.Resources["R02"].Deleted)
	require.False(t, state.Resources["R04"].Deleted)
	prior := len(f.deletes)
	f.base.journal.fail, f.failJournalOnDelete = false, ""
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
	assert.NotContains(t, f.deletes[prior:], f.base.plan.Storage.ID)
	state, err = f.base.journal.Load(t.Context())
	require.NoError(t, err)
	require.True(t, state.Resources["R02"].Deleted)
	require.True(t, state.CleanupComplete)
}

func TestOwnedCleanupSDKPurgePermissionFailureRetainsRecovery(t *testing.T) {
	f := newCleanupARM(t, true, true)
	f.deploy(t)
	f.denyPurge = true
	require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, true, nil), "AuthorizationFailed")
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	require.True(t, state.Foundry.AccountDeleted)
	require.False(t, state.Foundry.Purged)
	require.False(t, state.CleanupComplete)
	for _, assignment := range state.Assignments {
		require.False(t, assignment.Deleted, "failed requested Foundry purge precedes dependency cleanup")
	}
	f.denyPurge = false
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, true, nil))
	require.True(t, f.purged)
}
