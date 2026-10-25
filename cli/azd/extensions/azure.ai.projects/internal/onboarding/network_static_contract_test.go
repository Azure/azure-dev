// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestN02SubnetReadIgnoresAssociations(t *testing.T) {
	for _, scenario := range []string{"present", "missing IDs", "malformed IDs"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNetworkARMContract(t)
			subnetID := resourceValue(f.vnet.ID) + "/subnets/nodes"
			nsgID := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/networkSecurityGroups/agents"
			routeID := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/routeTables/agents"
			nsg, route := map[string]any{"id": nsgID}, map[string]any{"id": routeID}
			if scenario == "missing IDs" {
				delete(nsg, "id")
				delete(route, "id")
			} else if scenario == "malformed IDs" {
				nsg["id"], route["id"] = "invalid", "invalid"
			}
			var reads []string
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				reads = append(reads, request.URL.Path)
				if request.URL.Path != subnetID {
					return contractARMResponse(request, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed"}}`), nil
				}
				body := map[string]any{"id": subnetID, "properties": map[string]any{
					"provisioningState": "Succeeded", "addressPrefix": "10.0.0.0/24",
					"networkSecurityGroup": nsg, "routeTable": route,
				}}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				return contractARMResponse(request, http.StatusOK, string(raw)), nil
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
			subnet, report := checker.readNetworkSubnet(t.Context(), subnetID, "N02")
			require.NoError(t, report.Err())
			require.NotNil(t, subnet)
			prefixes, err := subnetPrefixes(subnet)
			require.NoError(t, err)
			require.Len(t, prefixes, 1)
			assert.Equal(t, "10.0.0.0/24", prefixes[0].String())
			assert.Equal(t, []string{subnetID}, reads, "association reads are outside basic subnet validation")
		})
	}
}

func TestN02OmittedSubnetDoesNotRequireLiveEvidenceOrPlan(t *testing.T) {
	f := newNetworkARMContract(t)
	addExternalNetworkTopology(f)
	plan := f.plan
	plan.Storage.Supplied = false
	plan.Registry.Supplied = false
	base := f.options()
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		for _, subnet := range f.vnet.Properties.Subnets {
			if strings.EqualFold(request.URL.Path, resourceValue(subnet.ID)) {
				f.methods = append(f.methods, request.Method+" "+request.URL.Path)
				raw, err := json.Marshal(subnet)
				require.NoError(t, err)
				return contractARMResponse(request, 200, string(raw)), nil
			}
		}
		return base.Transport.Do(request)
	})
	checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
	report := checker.networkChecks(t.Context(), plan, &f.fixture.Cluster, f.fixture.Pools, nil)
	found := false
	for _, check := range report {
		switch check.ID {
		case "N02":
			found = true
			assert.Equal(t, NotApplicable, check.Status)
		}
		assert.NotEqual(t, "N01", check.ID)
	}
	require.True(t, found)
	assert.Empty(t, f.methods, "omitted hosting and Storage selections need no network-source ARM reads")
	for _, request := range f.methods {
		assert.NotContains(t, request, "/subnets/azd-agents-", "preflight must not read a future subnet")
	}
}
