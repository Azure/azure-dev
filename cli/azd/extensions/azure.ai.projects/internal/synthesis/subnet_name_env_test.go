// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubnetNameEnvironmentResolution(t *testing.T) {
	const vnet = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/test"
	for _, field := range []string{"agentSubnet", "peSubnet"} {
		for _, tc := range []struct {
			name, value, resolved, wantError string
			env                              map[string]string
			preserve                         bool
		}{
			{name: "literal", value: "foundry-agent-e2e", resolved: "foundry-agent-e2e"},
			{name: "environment", value: "${BYO_SUBNET_NAME}", env: map[string]string{"BYO_SUBNET_NAME": "resolved"},
				resolved: "resolved"},
			{name: "process environment", value: "${BYO_PROCESS_SUBNET_NAME}", resolved: "process-subnet"},
			{name: "default", value: "${BYO_MISSING_SUBNET_NAME:-fallback}", resolved: "fallback"},
			{name: "empty default", value: "${BYO_MISSING_SUBNET_NAME:-}", wantError: "required after environment expansion"},
			{name: "missing", value: "${BYO_MISSING_SUBNET_NAME}", wantError: "unresolved environment variable"},
			{name: "empty", value: "${BYO_SUBNET_NAME}", env: map[string]string{"BYO_SUBNET_NAME": ""},
				wantError: "required after environment expansion"},
			{name: "whitespace", value: "${BYO_SUBNET_NAME}", env: map[string]string{"BYO_SUBNET_NAME": "  "},
				wantError: "required after environment expansion"},
			{name: "nested unresolved value", value: "${BYO_SUBNET_NAME}",
				env: map[string]string{"BYO_SUBNET_NAME": "${STILL_UNRESOLVED}"}, wantError: "unresolved references"},
			{name: "escaped", value: "$${BYO_SUBNET_NAME}", wantError: "unresolved references"},
			{name: "unsupported syntax", value: "${BYO_MISSING_SUBNET_NAME:=fallback}",
				wantError: "not a supported environment variable reference"},
			{name: "resource ID is not a name", value: vnet + "/subnets/agents", wantError: "expected a subnet name"},
			{name: "eject reference", value: "${BYO_SUBNET_NAME}", preserve: true, resolved: "${BYO_SUBNET_NAME}"},
			{name: "eject default", value: "${BYO_SUBNET_NAME:-fallback}", preserve: true,
				resolved: "${BYO_SUBNET_NAME:-fallback}"},
			{name: "eject unsupported syntax", value: "${BYO_SUBNET_NAME:=fallback}", preserve: true,
				wantError: "not a supported environment variable reference"},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				t.Setenv("BYO_PROCESS_SUBNET_NAME", "process-subnet")
				t.Setenv("BYO_MISSING_SUBNET_NAME", "")
				require.NoError(t, os.Unsetenv("BYO_MISSING_SUBNET_NAME"))
				network := &networkBlock{
					AgentSubnet: &subnetSpec{VNet: vnet, Name: "agents"},
					PESubnet:    &subnetSpec{VNet: vnet, Name: "pe"},
				}
				if field == "agentSubnet" {
					network.AgentSubnet.Name = tc.value
				} else {
					network.PESubnet.Name = tc.value
				}
				parameters, _, err := synthesizeNetwork(network, "project", tc.env, !tc.preserve)
				if tc.wantError != "" {
					require.ErrorContains(t, err, "services.project.network."+field+".name")
					require.ErrorContains(t, err, tc.wantError)
					return
				}
				require.NoError(t, err)
				key := map[string]string{"agentSubnet": "agentSubnetName", "peSubnet": "peSubnetName"}[field]
				assert.Equal(t, tc.resolved, parameters[key])
			})
		}
	}
}

func TestSubnetNameExpansionInlineAndReferencedProject(t *testing.T) {
	const vnet = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/test"
	for _, referenced := range []bool{false, true} {
		t.Run(map[bool]string{false: "inline", true: "ref"}[referenced], func(t *testing.T) {
			body := "host: azure.ai.project\nnetwork:\n" +
				"  agentSubnet: {vnet: '${BYO_VNET}', name: '${BYO_AGENT_NAME}'}\n" +
				"  peSubnet: {vnet: '${BYO_VNET}', name: '${BYO_PE_NAME}'}\n"
			projectRoot := t.TempDir()
			raw := "services:\n  project:\n    " + strings.ReplaceAll(strings.TrimSuffix(body, "\n"), "\n", "\n    ") + "\n"
			if referenced {
				require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "project.yaml"), []byte(body), 0o600))
				raw = "services:\n  project:\n    $ref: ./project.yaml\n"
			}
			input := Input{
				ServiceName: "project", RawAzureYAML: []byte(raw), ProjectRoot: projectRoot,
				Env: map[string]string{"BYO_VNET": vnet, "BYO_AGENT_NAME": "foundry-agent-e2e", "BYO_PE_NAME": "foundry-pe-e2e"},
			}
			result, err := Synthesize(input)
			require.NoError(t, err)
			assert.Equal(t, "foundry-agent-e2e", result.Parameters["agentSubnetName"])
			assert.Equal(t, "foundry-pe-e2e", result.Parameters["peSubnetName"])
			assert.Equal(t, vnet, result.Parameters["vnetId"])
			bicep := lookupBicep()
			t.Run("ARM injection", func(t *testing.T) {
				if bicep == "" {
					t.Skip("Bicep CLI is required for ARM network injection evaluation")
				}
				selection := evaluateHostingARM(t, bicep, result)
				assert.Equal(t, []any{map[string]any{
					"scenario": "agent", "subnetArmId": vnet + "/subnets/foundry-agent-e2e",
					"useMicrosoftManagedNetwork": false,
				}}, selection["networkInjections"])
			})
			input.Env["BYO_PE_NAME"] = "FOUNDRY-AGENT-E2E"
			_, err = Synthesize(input)
			require.ErrorContains(t, err, "agentSubnet.name and peSubnet.name must differ")
		})
	}
}
