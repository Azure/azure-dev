// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryPreparationJournal struct {
	data []byte
	fail bool
}

func (j *memoryPreparationJournal) Load(context.Context) (PreparationState, error) {
	var state PreparationState
	if len(j.data) == 0 {
		return state, nil
	}
	return state, json.Unmarshal(j.data, &state)
}

func (j *memoryPreparationJournal) Save(_ context.Context, state PreparationState) error {
	if j.fail {
		return fmt.Errorf("journal unavailable")
	}
	data, err := json.Marshal(state)
	if err == nil {
		j.data = data
	}
	return err
}

type verifiedPreparationNetwork struct{ called *int }

func (n verifiedPreparationNetwork) VerifyPreparedNetwork(
	_ context.Context, _ Plan, _ Prepared,
) (Report, error) {
	*n.called++
	return Report{{ID: "R06", Status: Pass, Mandatory: true, Observed: "fake source-bound network evidence"}}, nil
}

type preparationARM struct {
	t            *testing.T
	plan         Plan
	journal      *memoryPreparationJournal
	resources    map[string]map[string]any
	writes       []string
	failWorkload bool
	replication  bool
	replicated   bool
}

func storageContractBody(id string) map[string]any {
	return map[string]any{
		"id": id, "kind": "StorageV2", "properties": map[string]any{
			"accessTier": "Hot", "isHnsEnabled": false, "supportsHttpsTrafficOnly": true,
			"minimumTlsVersion": "TLS1_2", "allowSharedKeyAccess": false, "allowBlobPublicAccess": false,
			"provisioningState": "Succeeded",
		},
	}
}

func (f *preparationARM) options() *arm.ClientOptions {
	return contractARMOptions(func(request *http.Request) (*http.Response, error) {
		path := request.URL.Path
		key := strings.ToLower(path)
		respond := func(status int, body any) (*http.Response, error) {
			raw, err := json.Marshal(body)
			require.NoError(f.t, err)
			return contractARMResponse(request, status, string(raw)), nil
		}
		if strings.Contains(key, "/roledefinitions") {
			require.Equal(f.t, http.MethodGet, request.Method)
			return respond(200, map[string]any{"value": []any{map[string]any{
				"id": "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/" +
					"00000000-0000-0000-0000-000000000555",
				"properties": map[string]any{"roleName": "Managed Identity Federated Identity Credential Contributor",
					"type": "BuiltInRole"},
			}}})
		}
		if request.Method == http.MethodGet {
			if strings.HasSuffix(key, "/roleassignments") {
				var assignments []any
				for id, resource := range f.resources {
					if strings.HasPrefix(id, strings.TrimSuffix(key, "/roleassignments")+"/roleassignments/") {
						assignments = append(assignments, resource)
					}
				}
				return respond(200, map[string]any{"value": assignments})
			}
			if resource, exists := f.resources[key]; exists {
				return respond(200, resource)
			}
			return respond(404, map[string]any{"error": map[string]any{"code": "ResourceNotFound"}})
		}
		require.Equal(f.t, http.MethodPut, request.Method, "no deletes, patching, ListKeys or AKS changes")
		require.NotEqual(f.t, strings.ToLower(f.plan.Cluster.ID), key, "AKS must never be modified")
		state, err := f.journal.Load(request.Context())
		require.NoError(f.t, err)
		require.Equal(f.t, f.plan.Owner, state.Owner)
		intent := false
		for _, resource := range state.Resources {
			intent = intent || strings.EqualFold(resource.ID, path)
		}
		for _, assignment := range state.Assignments {
			intent = intent || strings.EqualFold(assignment.ID, path)
		}
		require.True(f.t, intent, "every remote write must have a durable prior ownership intent")
		if f.failWorkload && strings.EqualFold(path, f.plan.Workload.ID) {
			return respond(403, map[string]any{"error": map[string]any{"code": "AuthorizationFailed"}})
		}
		if strings.Contains(key, "/roleassignments/") && f.replication && !f.replicated {
			f.replicated = true
			return respond(400, map[string]any{"error": map[string]any{"code": "PrincipalNotFound"}})
		}
		raw, err := io.ReadAll(request.Body)
		require.NoError(f.t, err)
		var body map[string]any
		require.NoError(f.t, json.Unmarshal(raw, &body))
		body["id"] = path
		switch {
		case strings.Contains(key, "/userassignedidentities/") && !strings.Contains(key, "/roleassignments/"):
			principal, client := "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000011"
			if strings.EqualFold(path, f.plan.Workload.ID) {
				principal, client = "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000022"
			}
			body["properties"] = map[string]any{"principalId": principal, "clientId": client, "tenantId": "tenant"}
		case strings.Contains(key, "/roleassignments/"):
			props, ok := body["properties"].(map[string]any)
			require.True(f.t, ok)
			assert.Equal(f.t, "ServicePrincipal", props["principalType"])
			assert.Equal(f.t, f.plan.Owner, props["description"])
			props["scope"] = path[:strings.Index(key, "/providers/microsoft.authorization/roleassignments/")]
		case strings.Contains(key, "/storageaccounts/"):
			props, ok := body["properties"].(map[string]any)
			require.True(f.t, ok)
			props["provisioningState"] = "Succeeded"
			assert.Equal(f.t, "StorageV2", body["kind"])
			for _, field := range []string{"allowSharedKeyAccess", "allowBlobPublicAccess", "isHnsEnabled"} {
				assert.Equal(f.t, false, props[field])
			}
			assert.Equal(f.t, true, props["supportsHttpsTrafficOnly"])
			assert.Equal(f.t, "TLS1_2", props["minimumTlsVersion"])
		case strings.Contains(key, "/registries/"):
			props, ok := body["properties"].(map[string]any)
			require.True(f.t, ok)
			props["provisioningState"], props["loginServer"] = "Succeeded", "owned.azurecr.io"
			sku, ok := body["sku"].(map[string]any)
			require.True(f.t, ok)
			assert.Equal(f.t, "Premium", sku["name"])
		}
		f.resources[key] = body
		f.writes = append(f.writes, path)
		return respond(200, body)
	})
}

func preparationContractFixture(t *testing.T, storage, registry, abac bool) (*preparationARM, Facts) {
	t.Helper()
	input := contractPlanInput()
	if !storage {
		input.StorageID = ""
	}
	if !registry {
		input.RegistryID = ""
	}
	plan, err := BuildPlan(input)
	require.NoError(t, err)
	f := &preparationARM{t: t, plan: plan, journal: &memoryPreparationJournal{},
		resources: map[string]map[string]any{}}
	groupID := "/subscriptions/" + plan.SubscriptionID + "/resourceGroups/" + plan.ResourceGroup
	f.resources[strings.ToLower(groupID)] = map[string]any{"id": groupID, "location": "eastus"}
	if storage {
		f.resources[strings.ToLower(plan.Storage.ID)] = storageContractBody(plan.Storage.ID)
	}
	if registry {
		mode := "LegacyRegistryPermissions"
		if abac {
			mode = "AbacRepositoryPermissions"
		}
		f.resources[strings.ToLower(plan.Registry.ID)] = map[string]any{
			"id": plan.Registry.ID, "properties": map[string]any{
				"provisioningState": "Succeeded", "loginServer": "supplied.azurecr.io", "roleAssignmentMode": mode,
			},
		}
	}
	return f, Facts{TenantID: "tenant", KubeletObjectID: "00000000-0000-0000-0000-000000000003", RegistryABAC: abac}
}

func TestPreparationCRUDRecoveryAndRegistryModes(t *testing.T) {
	for _, storage := range []bool{false, true} {
		for _, registry := range []bool{false, true} {
			for _, abac := range []bool{false, true} {
				t.Run(fmt.Sprintf("storage=%t/registry=%t/abac=%t", storage, registry, abac), func(t *testing.T) {
					f, facts := preparationContractFixture(t, storage, registry, abac)
					calls := 0
					preparer := NewPreparerWithVerifier(contractCredential{}, f.options(), f.journal,
						verifiedPreparationNetwork{&calls})
					first, err := preparer.Prepare(t.Context(), f.plan, facts)
					require.NoError(t, err)
					assert.NotEqual(t, first.Hosting.PrincipalID, first.Workload.PrincipalID)
					assert.Equal(t, f.plan.Storage.ID, first.StorageID)
					assert.Equal(t, f.plan.Registry.ID, first.RegistryID)
					assert.Equal(t, abac, first.RegistryABAC)
					assert.Equal(t, 1, calls)
					writes := len(f.writes)
					second, err := preparer.Prepare(t.Context(), f.plan, facts)
					require.NoError(t, err)
					assert.Equal(t, first, second)
					assert.Len(t, f.writes, writes, "retry must reuse recorded identities and compatible grants")
					for _, path := range f.writes {
						if storage {
							assert.NotEqual(t, f.plan.Storage.ID, path)
						}
						if registry {
							assert.NotEqual(t, f.plan.Registry.ID, path)
						}
					}
					foundKubelet := false
					for path, resource := range f.resources {
						if !strings.Contains(path, "/roleassignments/") {
							continue
						}
						props, ok := resource["properties"].(map[string]any)
						require.True(t, ok)
						if props["principalId"] == facts.KubeletObjectID {
							foundKubelet = true
							assert.Contains(t, props["roleDefinitionId"], registryRoleID(abac))
							assert.Equal(t, f.plan.Registry.ID, props["scope"])
						}
					}
					assert.True(t, foundKubelet)
				})
			}
		}
	}
}

func TestPreparationPartialFailureResumesOnlyJournaledIdentities(t *testing.T) {
	f, facts := preparationContractFixture(t, true, true, false)
	f.failWorkload = true
	calls := 0
	preparer := NewPreparerWithVerifier(contractCredential{}, f.options(), f.journal, verifiedPreparationNetwork{&calls})
	_, err := preparer.Prepare(t.Context(), f.plan, facts)
	require.ErrorContains(t, err, "R05")
	assert.Zero(t, calls)
	state, err := f.journal.Load(t.Context())
	require.NoError(t, err)
	require.True(t, state.Resources["R04"].Complete)
	hosting := state.Resources["R04"].Identity
	assert.False(t, state.Resources["R05"].Complete)
	f.failWorkload, f.replication = false, true
	result, err := preparer.Prepare(t.Context(), f.plan, facts)
	require.NoError(t, err)
	assert.Equal(t, hosting, result.Hosting)
	assert.True(t, f.replicated)
	assert.Equal(t, 1, calls)
}

func TestPreparationOwnershipAndPersistenceFailuresWriteNothing(t *testing.T) {
	for _, failure := range []string{"journal", "customer identity", "owner mismatch", "unresolved network"} {
		t.Run(failure, func(t *testing.T) {
			f, facts := preparationContractFixture(t, true, true, false)
			switch failure {
			case "journal":
				f.journal.fail = true
			case "customer identity":
				f.resources[strings.ToLower(f.plan.Hosting.ID)] = map[string]any{
					"id": f.plan.Hosting.ID, "tags": map[string]string{preparationOwnerTag: f.plan.Owner},
				}
			case "owner mismatch":
				require.NoError(t, f.journal.Save(t.Context(), PreparationState{Owner: "another"}))
			case "unresolved network":
				f.plan.AgentSubnet.ID = ""
			}
			calls := 0
			preparer := NewPreparerWithVerifier(contractCredential{}, f.options(), f.journal,
				verifiedPreparationNetwork{&calls})
			_, err := preparer.Prepare(t.Context(), f.plan, facts)
			require.Error(t, err)
			assert.Empty(t, f.writes)
			assert.Zero(t, calls)
		})
	}
}

func TestPreparationSuppliedStorageUsesPreflightSafetySemantics(t *testing.T) {
	f, facts := preparationContractFixture(t, true, true, false)
	props, ok := f.resources[strings.ToLower(f.plan.Storage.ID)]["properties"].(map[string]any)
	require.True(t, ok)
	delete(props, "supportsHttpsTrafficOnly")
	delete(props, "allowBlobPublicAccess")
	props["minimumTlsVersion"] = "TLS1_3"
	calls := 0
	_, err := NewPreparerWithVerifier(contractCredential{}, f.options(), f.journal,
		verifiedPreparationNetwork{&calls}).Prepare(t.Context(), f.plan, facts)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestPrincipalRetryCancellationAndPermanentErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	err := retryPrincipalReplication(ctx, func() error {
		calls++
		cancel()
		return context.Canceled
	})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls)
	permanent := errors.New("permanent")
	require.ErrorIs(t, retryPrincipalReplication(t.Context(), func() error { return permanent }), permanent)
}
