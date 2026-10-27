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

func TestHostingNetworkSelectionEvaluatesGeneratedARM(t *testing.T) {
	bicep := lookupBicep()
	if bicep == "" {
		t.Skip("Bicep CLI is required to evaluate generated ARM expressions")
	}
	const vnet = "/subscriptions/network-sub/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/network"
	for _, choice := range []string{"none", "managed", "customer subnet", "created customer subnet"} {
		t.Run(choice, func(t *testing.T) {
			input := hostingContractInput("")
			var expected any
			if choice != "none" {
				input.RawAzureYAML = append(input.RawAzureYAML, []byte(
					"    network:\n      peSubnet: {vnet: "+vnet+", name: pe}\n")...)
				expected = []any{map[string]any{"scenario": "agent", "useMicrosoftManagedNetwork": true}}
			}
			if choice == "customer subnet" || choice == "created customer subnet" {
				prefix := ""
				if choice == "created customer subnet" {
					prefix = ", prefix: 10.0.1.0/24"
				}
				input.RawAzureYAML = append(input.RawAzureYAML, []byte(
					"      agentSubnet: {vnet: "+vnet+", name: legacy-agents"+prefix+"}\n")...)
				expected = []any{map[string]any{
					"scenario": "agent", "subnetArmId": vnet + "/subnets/legacy-agents",
					"useMicrosoftManagedNetwork": false,
				}}
			}
			result, err := Synthesize(input)
			require.NoError(t, err)
			selection := evaluateHostingARM(t, bicep, result)
			assert.Equal(t, expected, selection["networkInjections"])
			hosting, ok := selection["agentHostingConfigurations"].([]any)
			require.True(t, ok)
			require.Len(t, hosting, 1)
			entry, ok := hosting[0].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "ManagedCluster", entry["hostingType"])
			assert.Equal(t, "default", entry["name"])
			assert.Equal(t, contractClusterID, entry["clusterResourceId"])
			assert.Empty(t, entry["workloadIdentityResourceId"])
			assert.Empty(t, entry["hostingManagementIdentityResourceId"])
		})
	}
}

func TestHostingNetworkSelectionEvaluatesTerraformProperties(t *testing.T) {
	terraform, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("Terraform CLI is required to evaluate generated properties")
	}
	main, err := TerraformTemplatesFS().ReadFile("templates/terraform/main.tf")
	require.NoError(t, err)
	variables, err := TerraformTemplatesFS().ReadFile("templates/terraform/variables.tf")
	require.NoError(t, err)
	_, rest, found := strings.Cut(string(main), "    properties = ")
	require.True(t, found)
	expression, _, found := strings.Cut(rest, "\n  }\n  depends_on")
	require.True(t, found)
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			input := hostingContractInput("")
			result, err := Synthesize(input)
			require.NoError(t, err)
			hosting := hostingContractParameters(t, result)
			hosting["enabled"] = enabled
			values, err := json.Marshal(map[string]any{
				"subscription_id": "00000000-0000-0000-0000-000000000001", "location": "eastus",
				"resource_group_name": "test", "environment_name": "test",
				"agent_hosting": hosting,
			})
			require.NoError(t, err)
			dir := t.TempDir()
			for name, data := range map[string][]byte{
				"variables.tf": variables, "values.tfvars.json": values,
				"main.tf": []byte("locals {\nfoundry_account_name = \"test\"\n" +
					"hosting_identity_id = var.agent_hosting.hostingManagementIdentityResourceId\n" +
					"workload_identity_id = var.agent_hosting.workloadIdentityResourceId\n" +
					"hosting_storage_id = var.agent_hosting.storageAccountResourceId\n" +
					"hosting_cluster_id = var.agent_hosting.clusterResourceId\n" +
					"selected = " + expression + "\n}\n"),
			} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o600))
			}
			cmd := exec.CommandContext(t.Context(), terraform, "console", "-no-color", "-var-file=values.tfvars.json")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1", "TF_CLI_ARGS=",
				"TF_CLI_ARGS_console=", "TF_DATA_DIR="+filepath.Join(dir, ".terraform"))
			cmd.Stdin = strings.NewReader("jsonencode(local.selected)\n")
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", out)
			var decoded string
			require.NoError(t, json.Unmarshal(out, &decoded))
			var properties map[string]any
			require.NoError(t, json.Unmarshal([]byte(decoded), &properties))
			assert.Equal(t, enabled, properties["agentHostingConfigurations"] != nil)
			if enabled {
				configurations, ok := properties["agentHostingConfigurations"].([]any)
				require.True(t, ok)
				require.Len(t, configurations, 1)
				configuration, ok := configurations[0].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "ManagedCluster", configuration["hostingType"])
				assert.Equal(t, "default", configuration["name"])
			}
			assert.NotContains(t, properties, "networkInjections", "hosting never controls network injection")
		})
	}
}

// evaluateHostingARM projects only the real account expressions and their dependencies into a
// resource-free template. Native ARM evaluation cannot access Azure or create any resource.
func evaluateHostingARM(t *testing.T, bicep string, result *Result) map[string]any {
	t.Helper()
	type moduleTemplate struct {
		Definitions map[string]json.RawMessage
		Parameters  map[string]json.RawMessage
		Variables   map[string]json.RawMessage
		Resources   map[string]struct {
			Properties map[string]json.RawMessage
		}
	}
	var source struct {
		Resources map[string]struct {
			Properties struct {
				Template json.RawMessage
			}
		}
	}
	raw, err := TemplatesFS().ReadFile("templates/main.arm.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &source))
	var module moduleTemplate
	require.NoError(t, json.Unmarshal(source.Resources["resources"].Properties.Template, &module))
	account := module.Resources["foundryAccount"].Properties
	require.NotEmpty(t, account)
	parameters := map[string]json.RawMessage{}
	values := map[string]any{}
	var parameterFile strings.Builder
	parameterFile.WriteString("using './projection.json'\n")
	for _, name := range []string{
		"enableNetworkIsolation", "useManagedEgress", "vnetId", "agentSubnetName", "agentHosting",
	} {
		require.Contains(t, module.Parameters, name)
		parameters[name] = module.Parameters[name]
		if value, exists := result.Parameters[name]; exists {
			values[name] = value
			parameterFile.WriteString("param " + name + " = loadJsonContent('./values.json')." + name + "\n")
		}
	}
	variables := map[string]json.RawMessage{}
	for _, name := range []string{
		"useByoNetwork", "useManagedNetwork", "agentSubnetArmId", "agentNetworkInjections",
	} {
		require.Contains(t, module.Variables, name)
		variables[name] = module.Variables[name]
	}
	outputs := map[string]any{}
	for _, name := range []string{"networkInjections", "agentHostingConfigurations"} {
		require.Contains(t, account, name)
		var expression string
		require.NoError(t, json.Unmarshal(account[name], &expression))
		outputs[name] = map[string]any{
			"type": "object", "value": "[createObject('value'," + expression[1:len(expression)-1] + ")]",
		}
	}
	projection := map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"contentVersion": "1.0.0.0", "parameters": parameters,
		"languageVersion": "2.0", "definitions": module.Definitions,
		"variables": variables, "resources": map[string]any{}, "outputs": outputs,
	}
	dir := t.TempDir()
	for name, value := range map[string]any{"projection.json": projection, "values.json": values} {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o600))
	}
	params := filepath.Join(dir, "selection.bicepparam")
	require.NoError(t, os.WriteFile(params, []byte(parameterFile.String()), 0o600))
	//nolint:gosec // The discovered Bicep executable evaluates only a resource-free test-owned template.
	cmd := exec.CommandContext(t.Context(), bicep, "snapshot", params, "--mode", "Overwrite",
		"--subscription-id", "00000000-0000-0000-0000-000000000001", "--resource-group", "test", "--location", "eastus")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	//nolint:gosec // The snapshot path is fixed inside t.TempDir.
	snapshot, err := os.ReadFile(filepath.Join(dir, "selection.snapshot.json"))
	require.NoError(t, err)
	var evaluated struct {
		PredictedResources []any
		Diagnostics        []any
		Outputs            map[string]map[string]any
	}
	require.NoError(t, json.Unmarshal(snapshot, &evaluated))
	require.Empty(t, evaluated.PredictedResources)
	require.Empty(t, evaluated.Diagnostics)
	return map[string]any{
		"networkInjections":          evaluated.Outputs["networkInjections"]["value"],
		"agentHostingConfigurations": evaluated.Outputs["agentHostingConfigurations"]["value"],
	}
}
