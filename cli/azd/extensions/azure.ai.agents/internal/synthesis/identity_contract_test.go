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

func TestOptionalIdentitiesPreserveIndependentPublicSelections(t *testing.T) {
	for _, hosting := range []bool{false, true} {
		for _, workload := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "owned-hosting", true: "supplied-hosting"}[hosting],
				map[bool]string{false: "owned-workload", true: "supplied-workload"}[workload]}, "/"), func(t *testing.T) {
				plan := onboardingContractPlan()
				plan.HostingIdentitySupplied, plan.WorkloadIdentitySupplied = hosting, workload
				plan.CreateClusterGrants = !hosting
				plan.CreateFederationGrant = !hosting || !workload
				var extra strings.Builder
				for _, item := range []struct {
					supplied        bool
					field           string
					id, principalID *string
				}{
					{hosting, "hostingManagementIdentityResourceId",
						&plan.HostingIdentityResourceID, &plan.HostingPrincipalID},
					{workload, "workloadIdentityResourceId", &plan.WorkloadIdentityResourceID, &plan.WorkloadPrincipalID},
				} {
					if item.supplied {
						*item.id = strings.ReplaceAll(*item.id, "foundry-sub", "identity-sub")
						*item.id = strings.ReplaceAll(*item.id, "foundry-rg", item.field+"-rg")
						*item.principalID = "verified-" + item.field
						extra.WriteString("      " + item.field + ": " + *item.id + "\n")
					}
				}
				result, err := Synthesize(hostingContractInput(extra.String()))
				require.NoError(t, err)
				require.NoError(t, ApplyBYOOnboarding(result, plan))
				assert.Equal(t, plan, result.Parameters["byoOnboarding"])
				public := hostingContractParameters(t, result)
				assert.Equal(t, hosting, public["hostingManagementIdentityResourceId"] != "")
				assert.Equal(t, workload, public["workloadIdentityResourceId"] != "")
				if hosting {
					assert.Equal(t, plan.HostingIdentityResourceID, public["hostingManagementIdentityResourceId"])
				}
				if workload {
					assert.Equal(t, plan.WorkloadIdentityResourceID, public["workloadIdentityResourceId"])
				}
			})
		}
	}
}

func TestOptionalIdentityRejectsExplicitEmptyNullOrInvalidValues(t *testing.T) {
	for _, field := range []string{"hostingManagementIdentityResourceId", "workloadIdentityResourceId"} {
		for _, value := range []string{`""`, "null", `"   "`, "false", "123", "[]", "/wrong/type", `"/id?sig=secret"`} {
			t.Run(field+"/"+value, func(t *testing.T) {
				result, err := Synthesize(hostingContractInput("      " + field + ": " + value + "\n"))
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Contains(t, err.Error(), field)
			})
		}
	}
}

func TestCompiledARMIdentityCreationAndGrantSelection(t *testing.T) {
	bicep := lookupBicep()
	if bicep == "" {
		t.Skip("Bicep CLI is required for offline identity resource evaluation")
	}
	raw, err := ARMTemplate()
	require.NoError(t, err)
	var graph struct {
		Resources map[string]struct {
			Properties struct{ Template map[string]any }
		}
	}
	require.NoError(t, json.Unmarshal(raw, &graph))
	module := graph.Resources["prerequisites"].Properties.Template
	items, ok := module["resources"].([]any)
	require.True(t, ok)
	var identities []any
	for _, value := range items {
		resource, ok := value.(map[string]any)
		require.True(t, ok)
		if resource["type"] == "Microsoft.ManagedIdentity/userAssignedIdentities" {
			identities = append(identities, resource)
		}
		if resource["name"] == "byo-federation-access" {
			assert.Equal(t, "[parameters('plan').createFederationGrant]", resource["condition"])
			assert.Equal(t, "[variables('workloadParts')[2]]", resource["subscriptionId"])
			assert.Equal(t, "[variables('workloadParts')[4]]", resource["resourceGroup"])
		}
	}
	require.Len(t, identities, 2)
	for _, supplied := range []struct{ hosting, workload bool }{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		t.Run(strings.Join([]string{map[bool]string{false: "owned", true: "supplied"}[supplied.hosting],
			map[bool]string{false: "owned", true: "supplied"}[supplied.workload]}, "/"), func(t *testing.T) {
			plan := onboardingContractPlan()
			plan.HostingIdentitySupplied, plan.WorkloadIdentitySupplied = supplied.hosting, supplied.workload
			dir := t.TempDir()
			projection := map[string]any{
				"$schema": module["$schema"], "contentVersion": module["contentVersion"],
				"parameters": module["parameters"], "variables": module["variables"], "resources": identities,
			}
			template, err := json.Marshal(projection)
			require.NoError(t, err)
			parameters, err := json.Marshal(plan)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "projection.json"), template, 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "plan.json"), parameters, 0o600))
			params := filepath.Join(dir, "identity.bicepparam")
			require.NoError(t, os.WriteFile(params, []byte("using './projection.json'\nparam location = 'eastus'\n"+
				"param tags = {}\nparam plan = loadJsonContent('./plan.json')\n"), 0o600))
			//nolint:gosec // Bicep evaluates only local resources without deploying them.
			cmd := exec.CommandContext(t.Context(), bicep, "snapshot", params, "--mode", "Overwrite",
				"--subscription-id", "00000000-0000-0000-0000-000000000001",
				"--resource-group", "foundry-rg", "--location", "eastus")
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", out)
			//nolint:gosec // Fixed output filename under this test's temporary directory.
			data, err := os.ReadFile(filepath.Join(dir, "identity.snapshot.json"))
			require.NoError(t, err)
			var snapshot struct {
				PredictedResources []map[string]any
				Diagnostics        []any
			}
			require.NoError(t, json.Unmarshal(data, &snapshot))
			require.Empty(t, snapshot.Diagnostics)
			expected := 2
			if supplied.hosting {
				expected--
			}
			if supplied.workload {
				expected--
			}
			require.Len(t, snapshot.PredictedResources, expected, "supplied identities must have no PUT declaration")
		})
	}
}
