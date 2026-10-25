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

type preparationARM struct {
	t                *testing.T
	plan             Plan
	journal          *memoryPreparationJournal
	resources        map[string]map[string]any
	writes           []string
	graphGrantWrites []string
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
		require.Equal(f.t, http.MethodGet, request.Method, "planning and ownership observation never mutate ARM")
		key := strings.ToLower(request.URL.Path)
		respond := func(status int, body any) (*http.Response, error) {
			raw, err := json.Marshal(body)
			require.NoError(f.t, err)
			return contractARMResponse(request, status, string(raw)), nil
		}
		if strings.Contains(key, "/roledefinitions") {
			return respond(200, map[string]any{"value": []any{map[string]any{
				"id": "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/" +
					"00000000-0000-0000-0000-000000000555",
				"properties": map[string]any{"roleName": "Managed Identity Federated Identity Credential Contributor",
					"type": "BuiltInRole"},
			}}})
		}
		if strings.HasSuffix(key, "/roleassignments") {
			var assignments []any
			for id, resource := range f.resources {
				if strings.HasPrefix(id, key+"/") {
					assignments = append(assignments, resource)
				}
			}
			return respond(200, map[string]any{"value": assignments})
		}
		if strings.HasSuffix(key, "/userassignedidentities") {
			var identities []any
			for id, resource := range f.resources {
				if strings.HasPrefix(id, key+"/") && !strings.Contains(strings.TrimPrefix(id, key+"/"), "/") {
					identities = append(identities, resource)
				}
			}
			return respond(200, map[string]any{"value": identities})
		}
		if resource, exists := f.resources[key]; exists {
			return respond(200, resource)
		}
		return respond(404, map[string]any{"error": map[string]any{"code": "ResourceNotFound"}})
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
	plan.ResourceGroupSupplied = true
	plan, err = WithDeploymentOperations(plan, DeploymentSelection{Network: &LegacyNetwork{
		Enabled: true, VNetID: plan.AgentSubnet.Parent, AgentSubnetName: plan.AgentSubnet.Name, PESubnetName: "pe",
	}})
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

// materializeGraph models resources created by the single deployment, not SDK preparation calls.
func (f *preparationARM) materializeGraph(spec FoundrySpec) {
	data := spec.Deployment
	for index, item := range []struct {
		step     string
		resource Resource
	}{
		{"R04", f.plan.Hosting}, {"R05", f.plan.Workload},
	} {
		f.resources[strings.ToLower(item.resource.ID)] = map[string]any{
			"id": item.resource.ID, "tags": ownerTags(f.plan.Owner), "location": f.plan.Location,
			"properties": map[string]any{
				"principalId": fmt.Sprintf("00000000-0000-0000-0000-%012d", index+1),
				"clientId":    fmt.Sprintf("00000000-0000-0000-0000-%012d", (index+1)*11), "tenantId": "tenant",
			},
		}
	}
	if !f.plan.Storage.Supplied {
		body := storageContractBody(f.plan.Storage.ID)
		body["tags"] = ownerTags(f.plan.Owner)
		f.resources[strings.ToLower(f.plan.Storage.ID)] = body
	}
	if !f.plan.Registry.Supplied {
		f.resources[strings.ToLower(f.plan.Registry.ID)] = map[string]any{
			"id": f.plan.Registry.ID, "tags": ownerTags(f.plan.Owner),
			"properties": map[string]any{"provisioningState": "Succeeded", "roleAssignmentMode": "LegacyRegistryPermissions",
				"loginServer": "owned.azurecr.io"},
		}
	}
	grants := grantRequests(f.plan, Facts{RegistryABAC: data.RegistryABAC, KubeletObjectID: data.KubeletObjectID},
		Identity{PrincipalID: "00000000-0000-0000-0000-000000000001"},
		Identity{PrincipalID: "00000000-0000-0000-0000-000000000002"})
	grants = append(grants, grantRequest{checkID: "F01", scope: f.plan.Registry.ID,
		principalID: "00000000-0000-0000-0000-000000000004", roleID: registryRoleID(data.RegistryABAC)})
	for index, grant := range grants {
		if grant.checkID == "G04" && data.KubeletRoleAssignmentID != "" {
			continue
		}
		scope, err := arm.ParseResourceID(grant.scope)
		require.NoError(f.t, err)
		roleID := "/subscriptions/" + scope.SubscriptionID +
			"/providers/Microsoft.Authorization/roleDefinitions/" + grant.roleID
		if grant.checkID == "G02" {
			roleID = data.FederatedIdentityRoleDefinitionID
		}
		name := fmt.Sprintf("00000000-0000-0000-0000-%012d", index+100)
		id := grant.scope + "/providers/Microsoft.Authorization/roleAssignments/" + name
		f.graphGrantWrites = append(f.graphGrantWrites, id)
		f.resources[strings.ToLower(id)] = map[string]any{"id": id, "properties": map[string]any{
			"scope": grant.scope, "principalId": grant.principalID, "roleDefinitionId": roleID,
			"principalType": "ServicePrincipal", "description": f.plan.Owner,
		}}
	}
}
