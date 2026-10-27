// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func onboardingContractPlan() BYOOnboardingParameters {
	const parent = "/subscriptions/foundry-sub/resourceGroups/foundry-rg/providers/"
	return BYOOnboardingParameters{
		Owner: "0123456789abcdef", AccountName: "foundry-0123456789abcdef",
		ClusterResourceID:        contractClusterID,
		StorageAccountResourceID: parent + "Microsoft.Storage/storageAccounts/azd0123456789abcdef",
		StorageName:              "azd0123456789abcdef", StorageSKU: "Standard_ZRS",
		HostingIdentityName: "hosting-0123456789abcdef", WorkloadIdentityName: "workload-0123456789abcdef",
		HostingIdentityResourceID:  parent + "Microsoft.ManagedIdentity/userAssignedIdentities/hosting-0123456789abcdef",
		WorkloadIdentityResourceID: parent + "Microsoft.ManagedIdentity/userAssignedIdentities/workload-0123456789abcdef",
		CreateClusterGrants:        true, CreateFederationGrant: true, CreateStorageGrant: true,
		RegistryResourceID: parent + "Microsoft.ContainerRegistry/registries/azd0123456789abcdef",
		RegistryName:       "azd0123456789abcdef", KubeletObjectID: "kubelet-principal",
		FederatedIdentityRoleDefinitionID: "/subscriptions/foundry-sub/providers/" +
			"Microsoft.Authorization/roleDefinitions/federation-role",
	}
}

func TestApplyBYOOnboardingPreservesPlanAndPublicInput(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "reference"}[supplied], func(t *testing.T) {
			plan := onboardingContractPlan()
			plan.StorageSupplied, plan.RegistrySupplied, plan.ResourceGroupSupplied = supplied, supplied, supplied
			plan.RegistryABAC = supplied
			extra := ""
			if supplied {
				plan.StorageAccountResourceID = strings.ReplaceAll(plan.StorageAccountResourceID, "foundry-sub", "other-sub")
				plan.RegistryResourceID = strings.ReplaceAll(plan.RegistryResourceID, "foundry-rg", "other-rg")
				plan.KubeletRoleAssignmentID = plan.RegistryResourceID +
					"/providers/Microsoft.Authorization/roleAssignments/reused"
				extra = "      storageAccountResourceId: " + plan.StorageAccountResourceID + "\n"
			}
			result, err := Synthesize(hostingContractInput(extra))
			require.NoError(t, err)
			public := *result.Hosting
			require.NoError(t, ApplyBYOOnboarding(result, plan))
			assert.Equal(t, public, *result.Hosting)
			assert.Equal(t, plan, result.Parameters["byoOnboarding"])
			assert.Equal(t, true, result.Parameters["includeAcr"])
			hosting := hostingContractParameters(t, result)
			assert.Empty(t, hosting["hostingManagementIdentityResourceId"])
			assert.Empty(t, hosting["workloadIdentityResourceId"])
		})
	}
}

func TestApplyBYOOnboardingRejectsIncompletePlanAtomically(t *testing.T) {
	for name, change := range map[string]func(*BYOOnboardingParameters){
		"missing kubelet":      func(p *BYOOnboardingParameters) { p.KubeletObjectID = "" },
		"missing role":         func(p *BYOOnboardingParameters) { p.FederatedIdentityRoleDefinitionID = "" },
		"missing storage":      func(p *BYOOnboardingParameters) { p.StorageAccountResourceID = "" },
		"missing registry":     func(p *BYOOnboardingParameters) { p.RegistryResourceID = "" },
		"same identities":      func(p *BYOOnboardingParameters) { p.WorkloadIdentityName = p.HostingIdentityName },
		"wrong cluster":        func(p *BYOOnboardingParameters) { p.ClusterResourceID += "-other" },
		"wrong storage name":   func(p *BYOOnboardingParameters) { p.StorageName = "different" },
		"unsupported SKU":      func(p *BYOOnboardingParameters) { p.StorageSKU = "Premium_LRS" },
		"credential URL":       func(p *BYOOnboardingParameters) { p.RegistryResourceID += "?sig=secret" },
		"wrong existing grant": func(p *BYOOnboardingParameters) { p.KubeletRoleAssignmentID = "/unrelated" },
	} {
		t.Run(name, func(t *testing.T) {
			result, err := Synthesize(hostingContractInput(""))
			require.NoError(t, err)
			before, err := json.Marshal(result.Parameters)
			require.NoError(t, err)
			plan := onboardingContractPlan()
			change(&plan)
			require.Error(t, ApplyBYOOnboarding(result, plan))
			after, err := json.Marshal(result.Parameters)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after))
		})
	}

}

func TestApplyBYOOnboardingInheritedKubeletGrantScope(t *testing.T) {
	plan := onboardingContractPlan()
	const subscription = "/subscriptions/foundry-sub"
	for _, tc := range []struct {
		scope string
		valid bool
	}{
		{plan.RegistryResourceID, true},
		{subscription + "/resourceGroups/foundry-rg", true},
		{subscription, true},
		{subscription + "/resourceGroups/other-rg", false},
		{"/subscriptions/other-sub", false},
		{subscription + "/resourceGroups/foundry-rg/providers/Microsoft.Storage/storageAccounts/other", false},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			result, err := Synthesize(hostingContractInput(""))
			require.NoError(t, err)
			plan.KubeletRoleAssignmentID = tc.scope + "/providers/Microsoft.Authorization/roleAssignments/reused"
			err = ApplyBYOOnboarding(result, plan)
			if tc.valid {
				require.NoError(t, err)
				assert.Equal(t, plan, result.Parameters["byoOnboarding"])
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestApplyBYOOnboardingPreservesExplicitStorage(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "not supplied", true: "changed ID"}[changed], func(t *testing.T) {
			plan := onboardingContractPlan()
			input := hostingContractInput("      storageAccountResourceId: " + plan.StorageAccountResourceID + "\n")
			result, err := Synthesize(input)
			require.NoError(t, err)
			before, err := json.Marshal(result.Parameters)
			require.NoError(t, err)
			if changed {
				plan.StorageSupplied = true
				plan.StorageAccountResourceID += "other"
				plan.StorageName += "other"
			}
			require.ErrorContains(t, ApplyBYOOnboarding(result, plan), "preserve the explicitly supplied storage")
			after, err := json.Marshal(result.Parameters)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after))
		})
	}
}

func TestBYOOnboardingSingleARMGraph(t *testing.T) {
	raw, err := ARMTemplate()
	require.NoError(t, err)
	var graph map[string]any
	require.NoError(t, json.Unmarshal(raw, &graph))
	resources, ok := graph["resources"].(map[string]any)
	require.True(t, ok)
	preflight, ok := resources["prerequisites"].(map[string]any)
	require.True(t, ok)
	foundry, ok := resources["resources"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, foundry["dependsOn"], "prerequisites", "G01-G04 must finish before the account PUT")
	assert.NotContains(t, string(raw), "byoRegistryAccessReady")
	assert.NotContains(t, string(raw), "roleAssignmentManagedExternally")
	properties, ok := preflight["properties"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "[variables('hasOnboardingPlan')]", preflight["condition"])
	variables, ok := graph["variables"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "[and(parameters('agentHosting').enabled, not(empty(parameters('byoOnboarding'))))]",
		variables["hasOnboardingPlan"])
	foundryProperties, ok := foundry["properties"].(map[string]any)
	require.True(t, ok)
	foundryParameters, ok := foundryProperties["parameters"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "[if(variables('hasOnboardingPlan'), "+
		"createObject('value', parameters('byoOnboarding').registryResourceId), "+
		"createObject('value', parameters('containerRegistryResourceId')))]",
		foundryParameters["containerRegistryResourceId"])
	template, ok := properties["template"].(map[string]any)
	require.True(t, ok)
	items, ok := template["resources"].([]any)
	require.True(t, ok)
	types := map[string]int{}
	for _, item := range items {
		resource, ok := item.(map[string]any)
		require.True(t, ok)
		kind, ok := resource["type"].(string)
		require.True(t, ok)
		types[kind]++
		switch kind {
		case "Microsoft.Storage/storageAccounts":
			assert.Equal(t, "[not(parameters('plan').storageSupplied)]", resource["condition"])
			assert.Equal(t, "2023-05-01", resource["apiVersion"])
			assert.Equal(t, "StorageV2", resource["kind"])
			assert.Equal(t, map[string]any{"name": "[parameters('plan').storageSku]"}, resource["sku"])
			assert.Equal(t, map[string]any{
				"accessTier": "Hot", "isHnsEnabled": false, "publicNetworkAccess": "Enabled",
				"networkAcls":       map[string]any{"defaultAction": "Allow"},
				"minimumTlsVersion": "TLS1_2", "supportsHttpsTrafficOnly": true,
				"allowBlobPublicAccess": false, "allowSharedKeyAccess": false,
			}, resource["properties"])
		case "Microsoft.ContainerRegistry/registries":
			assert.Equal(t, "[not(parameters('plan').registrySupplied)]", resource["condition"])
			assert.Equal(t, "2025-03-01-preview", resource["apiVersion"])
			assert.Equal(t, map[string]any{"name": "Premium"}, resource["sku"])
			assert.Equal(t, map[string]any{
				"roleAssignmentMode": "LegacyRegistryPermissions",
				"adminUserEnabled":   false, "publicNetworkAccess": "Enabled",
			}, resource["properties"])
		}
	}
	assert.Equal(t, 2, types["Microsoft.ManagedIdentity/userAssignedIdentities"])
	assert.Equal(t, 1, types["Microsoft.Storage/storageAccounts"])
	assert.Equal(t, 1, types["Microsoft.ContainerRegistry/registries"])
	for _, file := range []string{
		"byo-role-assignment.bicep", "byo-identity-role-assignment.bicep",
		"byo-storage-role-assignment.bicep", "acr-pull-role-assignment.bicep",
	} {
		content, err := TemplatesFS().ReadFile("templates/modules/" + file)
		require.NoError(t, err)
		assert.Contains(t, string(content), "description: owner")
		assert.Contains(t, string(content), "principalType: 'ServicePrincipal'")
	}
	acr, err := TemplatesFS().ReadFile("templates/modules/acr.bicep")
	require.NoError(t, err)
	assert.Contains(t, string(acr), "if (reuseRegistry)")
	assert.Contains(t, string(acr), "scope: resourceGroup(split(registryResourceId, '/')[2]")
	assert.Contains(t, string(acr), "dependsOn: [\n        foundryAcrPull\n        preparedRegistryAccess")
	assert.Contains(t, string(acr), "resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01'")
	assert.Contains(t, string(acr), "publicNetworkAccess: enableNetworkIsolation ? 'Disabled' : 'Enabled'")
	assert.NotContains(t, string(acr), "roleAssignmentMode:")
}

func TestBYOOnboardingEvaluatesCreateOnlyResourceConditions(t *testing.T) {
	bicep := lookupBicep()
	if bicep == "" {
		t.Skip("Bicep CLI is required for offline ARM evaluation")
	}
	raw, err := ARMTemplate()
	require.NoError(t, err)
	var graph struct {
		Resources map[string]struct {
			Properties struct {
				Template map[string]any
			}
		}
	}
	require.NoError(t, json.Unmarshal(raw, &graph))
	module := graph.Resources["prerequisites"].Properties.Template
	items, ok := module["resources"].([]any)
	require.True(t, ok)
	var selected []any
	for _, item := range items {
		resource, ok := item.(map[string]any)
		require.True(t, ok)
		if resource["type"] == "Microsoft.Storage/storageAccounts" ||
			resource["type"] == "Microsoft.ContainerRegistry/registries" {
			selected = append(selected, resource)
		}
	}
	require.Len(t, selected, 2)
	projection := map[string]any{
		"$schema": module["$schema"], "contentVersion": module["contentVersion"],
		"parameters": module["parameters"], "resources": selected,
		"variables": module["variables"],
	}
	for _, storageSupplied := range []bool{false, true} {
		for _, registrySupplied := range []bool{false, true} {
			name := map[bool]string{false: "new-storage", true: "supplied-storage"}[storageSupplied] + "/" +
				map[bool]string{false: "new-registry", true: "supplied-abac-registry"}[registrySupplied]
			t.Run(name, func(t *testing.T) {
				plan := onboardingContractPlan()
				plan.StorageSupplied, plan.RegistrySupplied = storageSupplied, registrySupplied
				plan.RegistryABAC = registrySupplied
				dir := t.TempDir()
				template, err := json.Marshal(projection)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "projection.json"), template, 0o600))
				data, err := json.Marshal(plan)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "plan.json"), data, 0o600))
				params := filepath.Join(dir, "graph.bicepparam")
				require.NoError(t, os.WriteFile(params, []byte(
					"using './projection.json'\nparam location = 'eastus'\n"+
						"param tags = { team: 'test', 'azd-byoaks-owner': 'not-the-owner' }\n"+
						"param plan = loadJsonContent('./plan.json')\n"), 0o600))
				//nolint:gosec // Bicep snapshot evaluates local ARM; it does not submit a deployment.
				cmd := exec.CommandContext(t.Context(), bicep, "snapshot", params, "--mode", "Overwrite",
					"--subscription-id", "00000000-0000-0000-0000-000000000001",
					"--resource-group", "foundry-rg", "--location", "eastus")
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", out)
				//nolint:gosec // The snapshot filename is fixed beneath this test's t.TempDir.
				data, err = os.ReadFile(filepath.Join(dir, "graph.snapshot.json"))
				require.NoError(t, err)
				var snapshot struct {
					PredictedResources []map[string]any
					Diagnostics        []any
				}
				require.NoError(t, json.Unmarshal(data, &snapshot))
				require.Empty(t, snapshot.Diagnostics)
				expected := 2
				if storageSupplied {
					expected--
				}
				if registrySupplied {
					expected--
				}
				require.Len(t, snapshot.PredictedResources, expected,
					"supplied resources must have no create/update declaration")
				if expected > 0 {
					assert.Contains(t, string(data), plan.Owner)
					assert.NotContains(t, string(data), "not-the-owner")
				}
			})
		}
	}
}

func TestBYOOnboardingEvaluatesTerraformGrants(t *testing.T) {
	terraform, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("Terraform CLI is required for resource-free expression evaluation")
	}
	template, err := TerraformTemplatesFS().ReadFile("templates/terraform/byo-onboarding.tf")
	require.NoError(t, err)
	variables, err := TerraformTemplatesFS().ReadFile("templates/terraform/variables.tf")
	require.NoError(t, err)
	locals, _, found := strings.Cut(string(template), "\nresource ")
	require.True(t, found)
	locals = strings.ReplaceAll(locals, "azapi_resource.byo_identity", "local.mock_identity")
	for _, choice := range []string{
		"disabled", "create", "reference ABAC", "supplied hosting", "supplied workload", "supplied both",
	} {
		t.Run(choice, func(t *testing.T) {
			plan := onboardingContractPlan()
			var parameter any = plan
			expectedGrants := 5
			if choice == "disabled" {
				parameter, expectedGrants = nil, 0
			}
			if choice == "reference ABAC" {
				plan.RegistryABAC, plan.RegistrySupplied, plan.StorageSupplied = true, true, true
				plan.RegistryResourceID = strings.ReplaceAll(plan.RegistryResourceID, "foundry-sub", "registry-sub")
				plan.StorageAccountResourceID = strings.ReplaceAll(
					plan.StorageAccountResourceID, "foundry-sub", "storage-sub")
				plan.KubeletRoleAssignmentID = plan.RegistryResourceID +
					"/providers/Microsoft.Authorization/roleAssignments/reused"
				parameter, expectedGrants = plan, 4
			}
			if strings.HasPrefix(choice, "supplied") {
				plan.HostingIdentitySupplied = choice != "supplied workload"
				plan.WorkloadIdentitySupplied = choice != "supplied hosting"
				plan.HostingPrincipalID, plan.WorkloadPrincipalID = "customer-hosting", "customer-workload"
				plan.HostingIdentityResourceID = strings.ReplaceAll(
					plan.HostingIdentityResourceID, "foundry-rg", "hosting-rg")
				plan.WorkloadIdentityResourceID = strings.ReplaceAll(
					plan.WorkloadIdentityResourceID, "foundry-sub", "identity-sub")
				plan.CreateClusterGrants = !plan.HostingIdentitySupplied
				plan.CreateFederationGrant = !plan.HostingIdentitySupplied || !plan.WorkloadIdentitySupplied
				if !plan.CreateClusterGrants {
					expectedGrants -= 2
				}
				if !plan.CreateFederationGrant {
					expectedGrants--
				}
				parameter = plan
			}
			dir := t.TempDir()
			values, err := json.Marshal(map[string]any{
				"subscription_id": "foundry-sub", "location": "eastus", "resource_group_name": "foundry-rg",
				"environment_name": "test", "byo_onboarding": parameter,
			})
			require.NoError(t, err)
			mockIdentity := map[string]any{}
			for name, identity := range map[string]string{
				"hosting": plan.HostingIdentityName, "workload": plan.WorkloadIdentityName,
			} {
				mockIdentity[name] = map[string]any{
					"id": "/subscriptions/foundry-sub/resourceGroups/foundry-rg/providers/" +
						"Microsoft.ManagedIdentity/userAssignedIdentities/" + identity,
					"output": map[string]any{"properties": map[string]any{"principalId": name + "-principal"}},
				}
			}
			identities, err := json.Marshal(mockIdentity)
			require.NoError(t, err)
			for name, data := range map[string][]byte{
				"variables.tf": variables, "values.tfvars.json": values, "identities.json": identities,
				"main.tf": []byte(locals + "\nlocals {\nmock_identity = jsondecode(file(\"identities.json\"))\n}\n"),
			} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o600))
			}
			//nolint:gosec // Discovered Terraform evaluates only resource-free test locals.
			cmd := exec.CommandContext(t.Context(), terraform, "console", "-no-color", "-var-file=values.tfvars.json")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1", "TF_CLI_ARGS=",
				"TF_CLI_ARGS_console=", "TF_DATA_DIR="+filepath.Join(dir, ".terraform"))
			cmd.Stdin = strings.NewReader("jsonencode(local.byo_grants)\n")
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", out)
			var encoded string
			require.NoError(t, json.Unmarshal(out, &encoded))
			var grants map[string]map[string]string
			require.NoError(t, json.Unmarshal([]byte(encoded), &grants))
			require.Len(t, grants, expectedGrants)
			if expectedGrants == 0 {
				return
			}
			hostingPrincipal, workloadPrincipal := "hosting-principal", "workload-principal"
			if plan.HostingIdentitySupplied {
				hostingPrincipal = plan.HostingPrincipalID
			}
			if plan.WorkloadIdentitySupplied {
				workloadPrincipal = plan.WorkloadPrincipalID
			}
			if plan.CreateClusterGrants {
				assert.Equal(t, plan.ClusterResourceID, grants["cluster_reader"]["scope"])
				assert.Equal(t, hostingPrincipal, grants["cluster_extension"]["principal"])
			} else {
				assert.NotContains(t, grants, "cluster_reader")
				assert.NotContains(t, grants, "cluster_extension")
			}
			if plan.CreateFederationGrant {
				assert.Equal(t, plan.FederatedIdentityRoleDefinitionID, grants["federation"]["role"])
				assert.Equal(t, plan.WorkloadIdentityResourceID, grants["federation"]["scope"])
				assert.Equal(t, hostingPrincipal, grants["federation"]["principal"])
			} else {
				assert.NotContains(t, grants, "federation")
			}
			assert.Equal(t, plan.StorageAccountResourceID, grants["storage"]["scope"])
			assert.Equal(t, workloadPrincipal, grants["storage"]["principal"])
			if plan.KubeletRoleAssignmentID == "" {
				assert.Equal(t, plan.RegistryResourceID, grants["kubelet"]["scope"])
				assert.Equal(t, plan.KubeletObjectID, grants["kubelet"]["principal"])
				assert.True(t, strings.HasSuffix(grants["kubelet"]["role"], "7f951dda-4ed3-4680-a7ca-43fe172d538d"))
			} else {
				assert.NotContains(t, grants, "kubelet", "existing grants remain outside graph ownership")
			}
		})
	}
}

func TestBYOOnboardingEvaluatesDeterministicHostingIDs(t *testing.T) {
	bicep := lookupBicep()
	if bicep == "" {
		t.Skip("Bicep CLI is required for resource-free ARM evaluation")
	}
	raw, err := ARMTemplate()
	require.NoError(t, err)
	var graph map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &graph))
	for _, supplied := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "reference"}[supplied], func(t *testing.T) {
			plan := onboardingContractPlan()
			plan.ResourceGroupSupplied = supplied
			plan.StorageSupplied = supplied
			if supplied {
				plan.StorageAccountResourceID = strings.ReplaceAll(plan.StorageAccountResourceID, "foundry-sub", "other-sub")
				plan.HostingIdentityResourceID = strings.ReplaceAll(
					plan.HostingIdentityResourceID, "foundry-sub", "hosting-sub")
				plan.WorkloadIdentityResourceID = strings.ReplaceAll(
					plan.WorkloadIdentityResourceID, "foundry-rg", "workload-rg")
				plan.HostingIdentitySupplied, plan.WorkloadIdentitySupplied = true, true
				plan.HostingPrincipalID, plan.WorkloadPrincipalID = "hosting-principal", "workload-principal"
				plan.CreateClusterGrants, plan.CreateFederationGrant, plan.CreateStorageGrant = false, false, false
			}
			projection := map[string]any{
				"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
				"contentVersion": "1.0.0.0", "languageVersion": "2.0",
				"definitions": graph["definitions"], "parameters": graph["parameters"],
				"variables": graph["variables"], "resources": map[string]any{},
				"outputs": map[string]any{
					"hosting":    map[string]any{"type": "object", "value": "[variables('effectiveHosting')]"},
					"suppliedRG": map[string]any{"type": "bool", "value": "[variables('suppliedResourceGroup')]"},
				},
			}
			dir := t.TempDir()
			data, err := json.Marshal(projection)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "projection.json"), data, 0o600))
			data, err = json.Marshal(plan)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "plan.json"), data, 0o600))
			parameters := "using './projection.json'\nparam location = 'eastus'\n" +
				"param resourceGroupName = 'foundry-rg'\nparam foundryProjectName = 'project'\n" +
				"param byoOnboarding = loadJsonContent('./plan.json')\n" +
				"param agentHosting = { enabled: true, hostingType: 'ManagedCluster', name: 'aks', " +
				"clusterResourceId: '" + contractClusterID + "', hostingManagementIdentityResourceId: '', " +
				"workloadIdentityResourceId: '', storageAccountResourceId: '' }\n"
			params := filepath.Join(dir, "graph.bicepparam")
			require.NoError(t, os.WriteFile(params, []byte(parameters), 0o600))
			//nolint:gosec // Discovered Bicep evaluates only the resource-free projection.
			cmd := exec.CommandContext(t.Context(), bicep, "snapshot", params, "--mode", "Overwrite",
				"--subscription-id", "00000000-0000-0000-0000-000000000001",
				"--resource-group", "foundry-rg", "--location", "eastus")
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", out)
			//nolint:gosec // The snapshot filename is fixed beneath this test's t.TempDir.
			snapshot, err := os.ReadFile(filepath.Join(dir, "graph.snapshot.json"))
			require.NoError(t, err)
			var evaluated struct {
				PredictedResources []any
				Diagnostics        []any
				Outputs            map[string]any
			}
			require.NoError(t, json.Unmarshal(snapshot, &evaluated))
			require.Empty(t, evaluated.PredictedResources)
			require.Empty(t, evaluated.Diagnostics)
			hosting, ok := evaluated.Outputs["hosting"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "ManagedCluster", hosting["hostingType"])
			assert.Equal(t, "aks", hosting["name"])
			assert.Equal(t, plan.StorageAccountResourceID, hosting["storageAccountResourceId"])
			assert.Equal(t, plan.HostingIdentityResourceID, hosting["hostingManagementIdentityResourceId"])
			assert.Equal(t, plan.WorkloadIdentityResourceID, hosting["workloadIdentityResourceId"])
			assert.Equal(t, supplied, evaluated.Outputs["suppliedRG"])
		})
	}
}
