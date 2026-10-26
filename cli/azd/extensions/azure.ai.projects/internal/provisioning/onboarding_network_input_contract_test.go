// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"azure.ai.projects/internal/onboarding"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBYONetworkInputFailsBeforeAuthenticationOrWrites(t *testing.T) {
	for _, tc := range []struct {
		name, parameter string
		value           any
		expected        string
	}{
		{"omitted network", "enableNetworkIsolation", false, "requires network.agentSubnet and network.peSubnet"},
		{"managed egress", "useManagedEgress", true, "requires network.agentSubnet and network.peSubnet"},
		{"invalid VNet", "vnetId", "not-a-resource-id", "selected legacy network VNet"},
		{"missing Agent name", "agentSubnetName", "", "subnet name is invalid"},
		{"missing PE name", "peSubnetName", "", "subnet name is invalid"},
		{"unexpanded Agent name", "agentSubnetName", "${FOUNDRY_AGENT_SUBNET_NAME}", "subnet name is invalid"},
		{"unexpanded PE name", "peSubnetName", "${FOUNDRY_PE_SUBNET_NAME}", "subnet name is invalid"},
		{"same subnet", "peSubnetName", "agents", "must differ"},
	} {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/preview=%t", tc.name, preview), func(t *testing.T) {
				p := byoContractProvider(t)
				p.synthResult.Parameters[tc.parameter] = tc.value
				p.credential = nil
				p.virtualEnv = map[string]string{"preserve": "existing"}
				p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
					Transport: byoHTTPContract(func(*http.Request) (*http.Response, error) {
						t.Fatal("locally invalid network must not make any Azure request")
						return nil, nil
					}),
				}}
				p.byoPipeline = &onboarding.Pipeline{
					Resolve: func(context.Context, onboarding.Plan) (onboarding.Plan, error) {
						t.Fatal("network input validation must precede resource resolution")
						return onboarding.Plan{}, nil
					},
				}
				before, err := json.Marshal(p.synthResult.Parameters)
				require.NoError(t, err)
				if preview {
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}
				require.ErrorContains(t, err, tc.expected)
				assert.Equal(t, map[string]string{"preserve": "existing"}, p.virtualEnv)
				after, err := json.Marshal(p.synthResult.Parameters)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after))
				files, err := os.ReadDir(p.projectPath)
				require.NoError(t, err)
				assert.Empty(t, files, "invalid network must not write project files")
			})
		}
	}

}

func TestBYONetworkGateUsesEffectiveOnDiskParameters(t *testing.T) {
	for _, parameter := range []string{"enableNetworkIsolation", "useManagedEgress"} {
		t.Run(parameter, func(t *testing.T) {
			p := byoContractProvider(t)
			p.onDiskSource = &templateSource{
				mode: templateModeBicep, armTemplate: p.armTemplate, sourcePath: "offline-main.bicep",
				parameters: map[string]any{parameter: map[string]any{"value": parameter == "useManagedEgress"}},
			}
			p.credential = nil
			p.virtualEnv = map[string]string{"preserve": "existing"}
			err := p.planBYO(t.Context(), func(string) {}, false)
			require.ErrorContains(t, err, "requires network.agentSubnet and network.peSubnet")
			assert.Equal(t, map[string]string{"preserve": "existing"}, p.virtualEnv)
			assert.Nil(t, p.byoPlan, "valid synthesized defaults cannot override invalid effective parameters")
		})
	}
}
