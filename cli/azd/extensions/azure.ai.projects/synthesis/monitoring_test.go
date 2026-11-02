// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSynthesizeMonitoring(t *testing.T) {
	const component = "/subscriptions/00000000-0000-0000-0000-000000000001" +
		"/resourceGroups/monitoring/providers/Microsoft.Insights/components/traces"
	tests := []struct {
		name     string
		settings string
		existing bool
		env      map[string]string
		preserve bool
		mode     string
		id       string
		wantErr  string
	}{
		{name: "new project defaults on", mode: "create"},
		{name: "existing project defaults off", existing: true, mode: "none"},
		{name: "explicit opt out", settings: "monitoring:\n      enabled: false", mode: "none"},
		{
			name: "existing project opt in without ID is unsupported", existing: true,
			settings: "monitoring:\n      enabled: true", wantErr: "not supported",
		},
		{
			name: "existing project empty block is unsupported", existing: true,
			settings: "monitoring: {}", wantErr: "not supported",
		},
		{name: "new project empty block opts in", settings: "monitoring: {}", mode: "create"},
		{
			name: "BYO implies enabled", settings: "monitoring:\n      applicationInsightsResourceId: " + component,
			mode: "reuse-connect", id: component,
		},
		{
			name: "existing project BYO", existing: true,
			settings: "monitoring:\n      applicationInsightsResourceId: " + component,
			mode:     "reuse-connect", id: component,
		},
		{
			name: "selected environment", settings: "monitoring:\n      applicationInsightsResourceId: ${MONITORING_TEST_ID}",
			env: map[string]string{"MONITORING_TEST_ID": component}, mode: "reuse-connect", id: component,
		},
		{
			name: "preserved export reference", settings: "monitoring:\n      applicationInsightsResourceId: ${MONITORING_TEST_ID}",
			preserve: true, mode: "reuse-connect", id: "${MONITORING_TEST_ID}",
		},
		{
			name: "resolved empty is not create", settings: "monitoring:\n      applicationInsightsResourceId: ${MONITORING_TEST_ID}",
			env: map[string]string{"MONITORING_TEST_ID": ""}, wantErr: "nonempty",
		},
		{name: "literal empty is not create", settings: "monitoring:\n      applicationInsightsResourceId: ''", wantErr: "nonempty"},
		{
			name:     "process environment fallback",
			settings: "monitoring:\n      applicationInsightsResourceId: ${MONITORING_TEST_ID}",
			mode:     "reuse-connect", id: component,
		},
		{
			name: "default expression", settings: "monitoring:\n      applicationInsightsResourceId: ${UNSET_MONITORING_ID:-" +
				component + "}", env: map[string]string{"UNSET_MONITORING_ID": ""}, mode: "reuse-connect", id: component,
		},
		{
			name:     "explicit opt out wins over BYO",
			settings: "monitoring:\n      enabled: false\n      applicationInsightsResourceId: " + component,
			mode:     "none",
		},
		{name: "unknown field", settings: "monitoring:\n      enabld: true", wantErr: "unknown monitoring setting"},
		{name: "not an object", settings: "monitoring: true", wantErr: "monitoring must be an object"},
		{name: "null is not an object", settings: "monitoring: null", wantErr: "monitoring must be an object"},
		{name: "enabled type", settings: "monitoring:\n      enabled: 'true'", wantErr: "must be a boolean"},
		{name: "ID type", settings: "monitoring:\n      applicationInsightsResourceId: 42", wantErr: "must be a string"},
		{
			name: "wrong resource type", settings: "monitoring:\n      applicationInsightsResourceId: " +
				"/subscriptions/s/resourceGroups/r/providers/Microsoft.Storage/storageAccounts/a",
			wantErr: "Application Insights component",
		},
	}

	t.Setenv("MONITORING_TEST_ID", component)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint := ""
			if tt.existing {
				endpoint = "endpoint: https://account.services.ai.azure.com/api/projects/project\n    "
			}
			in := Input{
				RawAzureYAML: fmt.Appendf(nil,
					"services:\n  foundry:\n    host: azure.ai.project\n    %s%s\n", endpoint, tt.settings),
				ServiceName: "foundry", Env: tt.env, PreserveVarRefs: tt.preserve,
			}
			var result *Result
			var err error
			if tt.existing {
				result, err = SynthesizeExistingProject(in)
			} else {
				result, err = Synthesize(in)
			}
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.mode, result.Parameters["monitoringMode"])
			assert.Equal(t, tt.id, result.Parameters["applicationInsightsResourceId"])
		})
	}
}

func TestSynthesizeMonitoringPrivateNetwork(t *testing.T) {
	t.Parallel()
	const vnet = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/net/providers/" +
		"Microsoft.Network/virtualNetworks/vnet"
	const component = "/subscriptions/00000000-0000-0000-0000-000000000001" +
		"/resourceGroups/monitoring/providers/Microsoft.Insights/components/traces"
	networks := map[string]string{
		"managed egress": "    network:\n      isolationMode: AllowInternetOutbound\n" +
			"      peSubnet: {vnet: " + vnet + ", name: pe-subnet, prefix: 192.168.1.0/24}\n",
		"BYO egress": "    network:\n" +
			"      agentSubnet: {vnet: " + vnet + ", name: agent-subnet, prefix: 192.168.0.0/24}\n" +
			"      peSubnet: {vnet: " + vnet + ", name: pe-subnet, prefix: 192.168.1.0/24}\n",
	}
	monitoring := []struct {
		name     string
		settings string
		wantErr  bool
	}{
		{name: "default", wantErr: true},
		{name: "empty block", settings: "    monitoring: {}\n", wantErr: true},
		{name: "BYO component", settings: "    monitoring:\n      applicationInsightsResourceId: " + component + "\n", wantErr: true},
		{name: "explicit opt out", settings: "    monitoring:\n      enabled: false\n"},
	}
	for networkName, network := range networks {
		for _, tt := range monitoring {
			t.Run(networkName+" "+tt.name, func(t *testing.T) {
				t.Parallel()
				result, err := Synthesize(Input{
					RawAzureYAML: []byte("services:\n  foundry:\n    host: azure.ai.project\n" + network + tt.settings),
					ServiceName:  "foundry",
				})
				if tt.wantErr {
					require.ErrorContains(t, err, "does not configure private-network ingestion")
					require.ErrorContains(t, err, "monitoring.enabled: false")
					return
				}
				require.NoError(t, err)
				assert.Equal(t, "none", result.Parameters["monitoringMode"])
			})
		}
	}
}

func TestSynthesizeMonitoringInfrastructureAuthority(t *testing.T) {
	t.Parallel()
	for _, synthesize := range []struct {
		name string
		run  func(Input) (*Result, error)
	}{
		{"new project", Synthesize},
		{"existing project", SynthesizeExistingProject},
	} {
		t.Run(synthesize.name, func(t *testing.T) {
			endpoint := ""
			if synthesize.name == "existing project" {
				endpoint = "    endpoint: https://account.services.ai.azure.com/api/projects/project\n"
			}
			result, err := synthesize.run(Input{
				RawAzureYAML: []byte("services:\n  foundry:\n    host: azure.ai.project\n" + endpoint +
					"    monitoring: invalid-stale-yaml\n"),
				ServiceName: "foundry", InfrastructureOwnsMonitoring: true,
			})
			require.NoError(t, err)
			assert.NotContains(t, result.Parameters, "monitoringMode")
			assert.NotContains(t, result.Parameters, "applicationInsightsResourceId")
			assert.False(t, result.MonitoringExplicit)
		})
	}
}
