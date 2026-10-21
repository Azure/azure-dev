// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func addExternalNetworkTopology(f *networkARMContract) {
	parent := resourceValue(f.vnet.ID)
	for i, name := range []string{"GatewaySubnet", "RouteServerSubnet", "AzureFirewallSubnet"} {
		prefixes := []string{"10.0.2.0/27", "10.0.2.32/27", "10.0.2.64/26"}
		f.vnet.Properties.Subnets = append(f.vnet.Properties.Subnets, &armnetwork.Subnet{
			ID: new(parent + "/subnets/" + name), Name: new(name),
			Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: new(prefixes[i])},
		})
	}
	peer := networkBoundaryPeering()
	peer.Properties.AllowGatewayTransit, peer.Properties.UseRemoteGateways = new(true), new(true)
	f.vnet.Properties.VirtualNetworkPeerings = []*armnetwork.VirtualNetworkPeering{peer}
}

func TestDefaultNetworkScopeExcludesExternalInventoryDuringPlanningAndRecovery(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "interrupted-readback"}[interrupted], func(t *testing.T) {
			f := newNetworkARMContract(t)
			addExternalNetworkTopology(f)
			f.failAfterPut = interrupted
			plan := f.resolve()
			require.Equal(t, "10.0.1.0/27", plan.Network.AddressPrefix)
			p := &ownedPreparer{credential: contractCredential{}, options: onboardingARMOptions(f.options()), journal: f.journal}
			state, err := p.beginState(t.Context(), plan)
			require.NoError(t, err)
			err = p.prepareNetwork(t.Context(), plan, &state)
			if interrupted {
				require.ErrorContains(t, err, "ReadbackInterrupted")
			} else {
				require.NoError(t, err)
			}
			resumed := f.resolve()
			require.Equal(t, plan.Network, resumed.Network)
			state, err = p.beginState(t.Context(), resumed)
			require.NoError(t, err)
			require.NoError(t, p.prepareNetwork(t.Context(), resumed, &state))
			require.Equal(t, 1, f.writes)
			for _, request := range f.methods {
				for _, excluded := range []string{"virtualNetworkGateways", "expressRoute", "localNetworkGateways", "routeServers"} {
					assert.NotContains(t, request, excluded)
				}
			}
		})
	}
}

func TestGatewayAndRouteServerSubnetsRemainInCIDRInventory(t *testing.T) {
	for _, name := range []string{"GatewaySubnet", "RouteServerSubnet", "AzureFirewallSubnet"} {
		t.Run(name, func(t *testing.T) {
			parent := networkBoundaryVNet("10.2.0.0/24")
			parent.Properties.Subnets = []*armnetwork.Subnet{networkBoundarySubnet(name, "10.2.0.0/27")}
			peer := networkBoundaryPeering()
			peer.Properties.AllowGatewayTransit, peer.Properties.UseRemoteGateways = new(true), new(true)
			parent.Properties.VirtualNetworkPeerings = []*armnetwork.VirtualNetworkPeering{peer}
			occupied, err := networkOccupiedPrefixes(networkBoundaryCluster(), parent)
			require.NoError(t, err)
			require.Contains(t, occupied, netip.MustParsePrefix("10.2.0.0/27"))
			require.Contains(t, occupied, netip.MustParsePrefix("10.7.0.0/16"))
			chosen, err := allocateAgentPrefix(parent, occupied)
			require.NoError(t, err)
			require.Equal(t, "10.2.0.32/27", chosen.String())
			err = validateDefaultPrefix(*parent, SubnetPreparation{AddressPrefix: "10.2.0.0/27"}, false)
			require.ErrorContains(t, err, "overlaps")
		})
	}
}

func TestAgentSubnetScopeUsesARMInventoryNotExternalWorkloadDiscovery(t *testing.T) {
	for _, scenario := range []string{
		"valid", "missing provider", "sibling conflict", "gateway conflict", "direct peer conflict",
		"missing sibling prefix", "empty direct peer", "missing Service", "missing Overlay Pod",
		"unreadable VNet", "missing DNS", "missing routes", "missing policy", "missing TCP", "disconnected peer",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newNetworkARMContract(t)
			addExternalNetworkTopology(f)
			plan := f.plan
			plan.AgentSubnet = Resource{ID: resourceValue(f.vnet.ID) + "/subnets/azd-agents-supplied", Supplied: true}
			f.subnet = map[string]any{"id": plan.AgentSubnet.ID, "properties": map[string]any{
				"addressPrefix": "10.0.1.0/27", "provisioningState": "Succeeded",
				"delegations": []any{map[string]any{"properties": map[string]any{"serviceName": "Microsoft.App/environments"}}},
			}}
			switch scenario {
			case "sibling conflict", "gateway conflict":
				index := 0
				if scenario == "gateway conflict" {
					index = 1
				}
				f.vnet.Properties.Subnets[index].Properties.AddressPrefix = new("10.0.1.0/27")
			case "direct peer conflict":
				f.vnet.Properties.VirtualNetworkPeerings[0].Properties.RemoteAddressSpace = networkBoundaryAddressSpace("10.0.1.0/27")
			case "missing sibling prefix":
				f.vnet.Properties.Subnets[1].Properties.AddressPrefix = nil
			case "empty direct peer":
				f.vnet.Properties.VirtualNetworkPeerings[0].Properties.RemoteAddressSpace = networkBoundaryAddressSpace()
			case "disconnected peer":
				f.vnet.Properties.VirtualNetworkPeerings[0].Properties.PeeringState = new(armnetwork.VirtualNetworkPeeringStateDisconnected)
			case "missing Service":
				f.fixture.Cluster.Properties.NetworkProfile.ServiceCidr = nil
				f.fixture.Cluster.Properties.NetworkProfile.ServiceCidrs = nil
			case "missing Overlay Pod":
				f.fixture.Cluster.Properties.NetworkProfile.PodCidr = nil
				f.fixture.Cluster.Properties.NetworkProfile.PodCidrs = nil
			}
			base := f.options()
			forbidN02Reads := false
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method, "CIDR and workload evidence checks must stay read-only")
				if forbidN02Reads {
					require.False(t, strings.EqualFold(request.URL.Path, plan.AgentSubnet.ID))
					require.False(t, strings.EqualFold(request.URL.Path, resourceValue(f.vnet.ID)))
				}
				if scenario == "unreadable VNet" && strings.EqualFold(request.URL.Path, resourceValue(f.vnet.ID)) {
					return contractARMResponse(request, 403, `{"error":{"code":"AuthorizationFailed"}}`), nil
				}
				if strings.EqualFold(request.URL.Path, plan.Storage.ID) {
					raw, err := json.Marshal(storageContractBody(plan.Storage.ID))
					require.NoError(t, err)
					return contractARMResponse(request, 200, string(raw)), nil
				}
				if strings.EqualFold(request.URL.Path, plan.Registry.ID) {
					return contractARMResponse(request, 200, `{"properties":{"loginServer":"customer.azurecr.io",
						"publicNetworkAccess":"Enabled","dataEndpointEnabled":false}}`), nil
				}
				for _, subnet := range f.vnet.Properties.Subnets {
					if strings.EqualFold(request.URL.Path, resourceValue(subnet.ID)) {
						raw, err := json.Marshal(subnet)
						require.NoError(t, err)
						return contractARMResponse(request, 200, string(raw)), nil
					}
				}
				return base.Transport.Do(request)
			})
			requests := 0
			provider := networkEvidenceContract(func(_ context.Context, request NetworkRequest) (NetworkEvidence, error) {
				requests++
				require.Contains(t, []string{"S03", "R06"}, request.CheckID,
					"N02 must never request workload/source-bound evidence")
				evidence := registryPathEvidence(request)
				switch scenario {
				case "missing DNS":
					evidence.DNSVerified = false
				case "missing routes":
					evidence.EffectiveRoutesVerified = false
				case "missing policy":
					evidence.EffectivePolicyVerified = false
				case "missing TCP":
					evidence.TCP443Verified = false
				}
				return evidence, nil
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
			if scenario != "missing provider" {
				checker.evidence.Network = provider
			}
			for _, stage := range []string{"supplied-preflight", "prepared-readback"} {
				t.Run(stage, func(t *testing.T) {
					var report Report
					if stage == "supplied-preflight" {
						storage := &armstorage.Account{ID: new(plan.Storage.ID), Properties: &armstorage.AccountProperties{
							PrimaryEndpoints:    &armstorage.Endpoints{Blob: new("https://customer.blob.core.windows.net/")},
							PublicNetworkAccess: new(armstorage.PublicNetworkAccessEnabled),
							NetworkRuleSet:      &armstorage.NetworkRuleSet{DefaultAction: new(armstorage.DefaultActionAllow)},
						}}
						report, _ = checker.networkChecks(t.Context(), plan, &f.fixture.Cluster, f.fixture.Pools, storage)
					} else {
						forbidN02Reads = true
						report, _ = checker.VerifyPreparedNetwork(t.Context(), plan, Prepared{
							StorageID: plan.Storage.ID, RegistryID: plan.Registry.ID, AgentSubnetID: plan.AgentSubnet.ID,
						})
					}
					var n02 Report
					for _, check := range report {
						assert.NotEqual(t, "N01", check.ID)
						if check.ID == "N02" {
							n02 = append(n02, check)
						}
					}
					if stage == "prepared-readback" {
						require.Empty(t, n02, "R06 must not rerun or report N02")
						if strings.HasPrefix(scenario, "missing ") && scenario != "missing sibling prefix" &&
							scenario != "missing Service" && scenario != "missing Overlay Pod" {
							require.Error(t, report.Err(), "independent S03/registry requirements remain enforced")
						}
						return
					}
					require.NotEmpty(t, n02)
					switch scenario {
					case "valid", "missing provider", "missing DNS", "missing routes", "missing policy", "missing TCP":
						require.NoError(t, n02.Err(), "N02 needs only valid ARM static configuration")
						if scenario == "missing provider" {
							require.Error(t, report.Err(), "S03 must still require its supported evidence provider")
						} else {
							require.Positive(t, requests)
						}
					default:
						require.Error(t, n02.Err(), "required in-scope ARM metadata remains mandatory")
						if scenario == "unreadable VNet" {
							cause, ok := errors.AsType[*azcore.ResponseError](n02.Err())
							require.True(t, ok)
							assert.Equal(t, 403, cause.StatusCode)
						}
					}
				})
			}
		})
	}
}
