// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestN02StaticAssociatedConfiguration(t *testing.T) {
	for _, scenario := range []string{
		"valid NVA", "missing NSG rules", "invalid NSG action", "incomplete NSG selector",
		"invalid route", "incomplete route", "invalid NVA hop", "unreadable association",
		"missing association ID", "wrong association ID",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newNetworkARMContract(t)
			subnetID := resourceValue(f.vnet.ID) + "/subnets/nodes"
			nsgID := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/networkSecurityGroups/agents"
			routeID := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/routeTables/agents"
			security := map[string]any{
				"priority": 100, "direction": "Outbound", "access": "Allow", "protocol": "*",
				"sourceAddressPrefix": "*", "destinationAddressPrefix": "*",
				"sourcePortRange": "*", "destinationPortRange": "*",
			}
			route := map[string]any{
				"addressPrefix": "0.0.0.0/0", "nextHopType": "VirtualAppliance", "nextHopIpAddress": "10.0.2.70",
			}
			nsg := map[string]any{
				"provisioningState": "Succeeded", "securityRules": []any{map[string]any{"properties": security}},
			}
			switch scenario {
			case "missing NSG rules":
				delete(nsg, "securityRules")
			case "invalid NSG action":
				security["access"] = "invalid"
			case "incomplete NSG selector":
				delete(security, "destinationPortRange")
			case "invalid route":
				route["addressPrefix"] = "10.0.0.1/24"
			case "incomplete route":
				delete(route, "nextHopType")
			case "invalid NVA hop":
				route["nextHopIpAddress"] = "not-an-IP"
			}
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				var body any
				switch request.URL.Path {
				case subnetID:
					association := map[string]any{"id": nsgID}
					if scenario == "missing association ID" {
						delete(association, "id")
					}
					body = map[string]any{"id": subnetID, "properties": map[string]any{
						"provisioningState": "Succeeded", "addressPrefix": "10.0.0.0/24",
						"networkSecurityGroup": association, "routeTable": map[string]any{"id": routeID},
					}}
				case nsgID:
					if scenario == "unreadable association" {
						return contractARMResponse(request, 403, `{"error":{"code":"AuthorizationFailed"}}`), nil
					}
					returnedID := nsgID
					if scenario == "wrong association ID" {
						returnedID += "-different"
					}
					body = map[string]any{"id": returnedID, "properties": nsg}
				case routeID:
					body = map[string]any{"id": routeID, "properties": map[string]any{
						"provisioningState": "Succeeded", "routes": []any{map[string]any{"properties": route}},
					}}
				default:
					t.Fatalf("unexpected external discovery request: %s", request.URL.Path)
				}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				return contractARMResponse(request, 200, string(raw)), nil
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
			_, report := checker.readNetworkSubnet(t.Context(), subnetID, "N02")
			if scenario == "valid NVA" {
				require.NoError(t, report.Err(), "ARM-visible NVA route is sufficient; no internal policy/probe is requested")
			} else {
				require.Error(t, report.Err())
			}
			if scenario == "unreadable association" {
				cause, ok := errors.AsType[*azcore.ResponseError](report.Err())
				require.True(t, ok)
				assert.Equal(t, 403, cause.StatusCode)
			}
		})
	}
}

func TestN02StaticStoragePrivateEndpointDNS(t *testing.T) {
	for _, scenario := range []string{
		"managed DNS", "external DNS", "missing Storage settings", "pending approval",
		"missing link metadata", "invalid DNS address", "missing DNS record data", "missing DNS zone config",
		"unreadable records", "unready endpoint",
		"invalid Storage firewall",
	} {
		t.Run(scenario, func(t *testing.T) {
			plan, err := BuildPlan(contractPlanInput())
			require.NoError(t, err)
			root := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/"
			endpointID, zoneID := root+"privateEndpoints/blob", root+"privateDnsZones/privatelink.blob.core.windows.net"
			storage := &armstorage.Account{ID: new(plan.Storage.ID), Properties: &armstorage.AccountProperties{
				PublicNetworkAccess: new(armstorage.PublicNetworkAccessDisabled),
				NetworkRuleSet:      &armstorage.NetworkRuleSet{DefaultAction: new(armstorage.DefaultActionDeny)},
				PrivateEndpointConnections: []*armstorage.PrivateEndpointConnection{{Properties: &armstorage.
					PrivateEndpointConnectionProperties{
					PrivateEndpoint: &armstorage.PrivateEndpoint{ID: new(endpointID)},
					PrivateLinkServiceConnectionState: &armstorage.PrivateLinkServiceConnectionState{
						Status: new(armstorage.PrivateEndpointServiceConnectionStatusApproved),
					},
				}}},
			}}
			switch scenario {
			case "missing Storage settings":
				storage.Properties.NetworkRuleSet = nil
			case "pending approval":
				storage.Properties.PrivateEndpointConnections[0].Properties.PrivateLinkServiceConnectionState.Status =
					new(armstorage.PrivateEndpointServiceConnectionStatusPending)
			case "invalid Storage firewall":
				storage.Properties.NetworkRuleSet.IPRules = []*armstorage.IPRule{{IPAddressOrRange: new("invalid")}}
			}
			reads := []string{}
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				reads = append(reads, request.URL.Path)
				var body any
				switch {
				case strings.EqualFold(request.URL.Path, endpointID):
					state := "Succeeded"
					if scenario == "unready endpoint" {
						state = "Updating"
					}
					body = map[string]any{"id": endpointID, "properties": map[string]any{"provisioningState": state}}
				case strings.HasSuffix(request.URL.Path, "/privateDnsZoneGroups"):
					values := []any{}
					if scenario != "external DNS" {
						values = append(values, map[string]any{"properties": map[string]any{
							"privateDnsZoneConfigs": []any{map[string]any{"properties": map[string]any{"privateDnsZoneId": zoneID}}},
						}})
					}
					if scenario == "missing DNS zone config" {
						values = []any{map[string]any{"properties": map[string]any{}}}
					}
					body = map[string]any{"value": values}
				case strings.EqualFold(request.URL.Path, zoneID):
					body = map[string]any{"id": zoneID, "properties": map[string]any{"provisioningState": "Succeeded"}}
				case strings.HasSuffix(request.URL.Path, "/virtualNetworkLinks"):
					properties := map[string]any{
						"provisioningState": "Succeeded", "virtualNetwork": map[string]any{"id": root + "virtualNetworks/aks"},
					}
					if scenario == "missing link metadata" {
						delete(properties, "virtualNetwork")
					}
					body = map[string]any{"value": []any{map[string]any{"properties": properties}}}
				case strings.HasSuffix(request.URL.Path, "/ALL"):
					if scenario == "unreadable records" {
						return contractARMResponse(request, 403, `{"error":{"code":"AuthorizationFailed"}}`), nil
					}
					ip := "10.0.3.4"
					if scenario == "invalid DNS address" {
						ip = "not-an-IP"
					}
					properties := map[string]any{"ttl": 300, "aRecords": []any{map[string]any{"ipv4Address": ip}}}
					if scenario == "missing DNS record data" {
						delete(properties, "aRecords")
					}
					body = map[string]any{"value": []any{map[string]any{
						"name": "storage", "type": "Microsoft.Network/privateDnsZones/A",
						"properties": properties,
					}}}
				default:
					t.Fatalf("unexpected external DNS/source probe: %s", request.URL.Path)
				}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				return contractARMResponse(request, 200, string(raw)), nil
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
			report := checker.staticStorageNetwork(t.Context(), storage)
			if scenario == "managed DNS" || scenario == "external DNS" {
				require.NoError(t, report.Err())
				require.Positive(t, len(reads))
			} else {
				require.Error(t, report.Err())
			}
			if scenario == "unreadable records" {
				cause, ok := errors.AsType[*azcore.ResponseError](report.Err())
				require.True(t, ok)
				assert.Equal(t, 403, cause.StatusCode)
			}
			for _, check := range report {
				require.Equal(t, "N02", check.ID)
			}
		})
	}
}

func TestN02PlannedSubnetPassesWithoutLiveEvidence(t *testing.T) {
	f := newNetworkARMContract(t)
	addExternalNetworkTopology(f)
	plan := f.resolve()
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
	report, _ := checker.networkChecks(t.Context(), plan, &f.fixture.Cluster, f.fixture.Pools, nil)
	found := false
	for _, check := range report {
		switch check.ID {
		case "N02":
			found = true
			assert.Equal(t, Pass, check.Status)
		}
		assert.NotEqual(t, "N01", check.ID)
	}
	require.True(t, found)
	for _, request := range f.methods {
		assert.NotContains(t, request, "/subnets/azd-agents-", "preflight must not read a future subnet")
	}
}
