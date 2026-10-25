// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type storageNetworkContract struct {
	base    *networkARMContract
	account *armstorage.Account
	bodies  map[string]any
	reads   []string
	errors  map[string]int
}

func newStorageNetworkContract(t *testing.T) *storageNetworkContract {
	t.Helper()
	base := newNetworkARMContract(t)
	base.plan.Registry.Supplied = false
	account := &armstorage.Account{ID: new(base.plan.Storage.ID), Properties: &armstorage.AccountProperties{
		PrimaryEndpoints:    &armstorage.Endpoints{Blob: new("https://customer.blob.core.windows.net/")},
		PublicNetworkAccess: new(armstorage.PublicNetworkAccessEnabled),
		NetworkRuleSet:      &armstorage.NetworkRuleSet{DefaultAction: new(armstorage.DefaultActionAllow)},
	}}
	source := resourceValue(base.fixture.Pools[1].Properties.VnetSubnetID)
	return &storageNetworkContract{base: base, account: account, errors: map[string]int{},
		bodies: map[string]any{source: base.vnet.Properties.Subnets[0], resourceValue(base.vnet.ID): base.vnet}}
}

func (f *storageNetworkContract) checker() *sdkResourceChecker {
	return &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(contractARMOptions(
		func(r *http.Request) (*http.Response, error) {
			require.Equal(f.base.t, http.MethodGet, r.Method)
			f.reads = append(f.reads, r.URL.Path)
			if status, ok := f.errors[r.URL.Path]; ok {
				code := "AuthorizationFailed"
				if status == 404 {
					code = "ResourceNotFound"
				}
				return contractARMResponse(r, status, `{"error":{"code":"`+code+`"}}`), nil
			}
			body, ok := f.bodies[r.URL.Path]
			switch {
			case strings.Contains(strings.ToLower(r.URL.Path), "/providers/microsoft.compute/skus"):
				body, ok = map[string]any{"value": f.base.fixture.SKUs}, true
			case strings.Count(r.URL.Path, "/") == 2 && strings.HasPrefix(r.URL.Path, "/subscriptions/"):
				body, ok = map[string]any{"tenantId": "tenant"}, true
			case strings.EqualFold(r.URL.Path, f.base.plan.Cluster.ID):
				body, ok = f.base.fixture.Cluster, true
			case strings.HasSuffix(r.URL.Path, "/agentPools"):
				body, ok = map[string]any{"value": f.base.fixture.Pools}, true
			case strings.EqualFold(r.URL.Path, f.base.plan.Storage.ID):
				body, ok = f.account, true
			}
			require.True(f.base.t, ok, "unexpected S03 read: %s", r.URL.Path)
			raw, err := json.Marshal(body)
			require.NoError(f.base.t, err)
			return contractARMResponse(r, 200, string(raw)), nil
		}))}
}

func (f *storageNetworkContract) restrictTo(source string) {
	f.account.Properties.NetworkRuleSet = &armstorage.NetworkRuleSet{
		DefaultAction: new(armstorage.DefaultActionDeny),
		VirtualNetworkRules: []*armstorage.VirtualNetworkRule{{
			VirtualNetworkResourceID: new(source), State: new(armstorage.StateSucceeded),
		}},
	}
	subnet, ok := f.bodies[source].(*armnetwork.Subnet)
	require.True(f.base.t, ok)
	subnet.Properties.ServiceEndpoints = []*armnetwork.ServiceEndpointPropertiesFormat{{
		Service: new("Microsoft.Storage"), ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
	}}
}

func (f *storageNetworkContract) privatePath() (string, string) {
	f.account.Properties.PublicNetworkAccess = new(armstorage.PublicNetworkAccessDisabled)
	vnet := resourceValue(f.base.vnet.ID)
	pe := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/privateEndpoints/blob"
	zone := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/privateDnsZones/" +
		"privatelink.blob.core.windows.net"
	f.account.Properties.PrivateEndpointConnections = []*armstorage.PrivateEndpointConnection{{
		Properties: &armstorage.PrivateEndpointConnectionProperties{
			ProvisioningState: new(armstorage.PrivateEndpointConnectionProvisioningStateSucceeded),
			PrivateEndpoint:   &armstorage.PrivateEndpoint{ID: new(pe)},
			PrivateLinkServiceConnectionState: &armstorage.PrivateLinkServiceConnectionState{
				Status: new(armstorage.PrivateEndpointServiceConnectionStatusApproved),
			},
		},
	}}
	f.bodies[pe] = map[string]any{"id": pe, "properties": map[string]any{
		"provisioningState": "Succeeded", "subnet": map[string]any{"id": vnet + "/subnets/endpoints"},
		"privateLinkServiceConnections": []any{map[string]any{"properties": map[string]any{
			"privateLinkServiceId": f.base.plan.Storage.ID, "groupIds": []string{"blob"},
		}}},
	}}
	f.bodies[pe+"/privateDnsZoneGroups"] = map[string]any{"value": []any{map[string]any{
		"properties": map[string]any{"provisioningState": "Succeeded", "privateDnsZoneConfigs": []any{
			map[string]any{"properties": map[string]any{"privateDnsZoneId": zone}},
		}},
	}}}
	f.bodies[zone] = map[string]any{"id": zone, "properties": map[string]any{"provisioningState": "Succeeded"}}
	f.bodies[zone+"/virtualNetworkLinks"] = map[string]any{"value": []any{map[string]any{
		"properties": map[string]any{"provisioningState": "Succeeded", "virtualNetworkLinkState": "Completed",
			"virtualNetwork": map[string]any{"id": vnet}},
	}}}
	return pe, zone
}

func TestStoragePublicBaselineAndSkippedTopologies(t *testing.T) {
	for _, scenario := range []string{
		"public", "new", "node service endpoint", "pod service endpoint", "global service endpoint",
		"same VNet private endpoint", "direct peering", "one-sided peering", "IP rule", "NAT", "custom DNS", "hub spoke",
		"pending approval", "rejected", "rejected alongside valid", "failed endpoint", "failed rule", "wrong DNS link",
		"deleted firewall source",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newStorageNetworkContract(t)
			source := resourceValue(f.base.fixture.Pools[1].Properties.VnetSubnetID)
			expected := NotApplicable
			switch scenario {
			case "public":
				expected = Pass
			case "new":
				f.base.plan.Storage.Supplied = false
			case "node service endpoint", "global service endpoint":
				f.restrictTo(source)
				if scenario == "global service endpoint" {
					f.base.vnet.Properties.Subnets[0].Properties.ServiceEndpoints[0].Service = new("Microsoft.Storage.Global")
				}
			case "pod service endpoint":
				pod := resourceValue(f.base.vnet.ID) + "/subnets/pods"
				f.base.fixture.Pools[1].Properties.PodSubnetID = new(pod)
				f.bodies[pod] = &armnetwork.Subnet{ID: new(pod), Properties: &armnetwork.SubnetPropertiesFormat{
					ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
				}}
				f.restrictTo(pod)
			case "IP rule", "NAT":
				f.account.Properties.NetworkRuleSet.DefaultAction = new(armstorage.DefaultActionDeny)
				if scenario == "IP rule" {
					f.account.Properties.NetworkRuleSet.IPRules = []*armstorage.IPRule{{IPAddressOrRange: new("10.0.0.1")}}
				} else {
					f.base.vnet.Properties.Subnets[0].Properties.NatGateway =
						&armnetwork.SubResource{ID: new("/custom/nat")}
				}
			case "failed rule", "deleted firewall source":
				f.restrictTo(source)
				f.account.Properties.NetworkRuleSet.VirtualNetworkRules[0].State = new(armstorage.StateFailed)
				if scenario == "deleted firewall source" {
					f.account.Properties.NetworkRuleSet.VirtualNetworkRules[0].State = new(armstorage.StateNetworkSourceDeleted)
				}
			default:
				pe, zone := f.privatePath()
				switch scenario {
				case "custom DNS":
					f.base.vnet.Properties.DhcpOptions = &armnetwork.DhcpOptions{DNSServers: []*string{new("10.0.0.4")}}
				case "hub spoke":
					f.bodies[pe].(map[string]any)["properties"].(map[string]any)["subnet"] =
						map[string]any{"id": strings.Replace(source, "/aks/subnets/nodes", "/remote/subnets/pe", 1)}
				case "pending approval":
					f.account.Properties.PrivateEndpointConnections[0].Properties.PrivateLinkServiceConnectionState.Status =
						new(armstorage.PrivateEndpointServiceConnectionStatusPending)
				case "rejected":
					f.account.Properties.PrivateEndpointConnections[0].Properties.PrivateLinkServiceConnectionState.Status =
						new(armstorage.PrivateEndpointServiceConnectionStatusRejected)
				case "rejected alongside valid":
					f.account.Properties.PrivateEndpointConnections = append(
						f.account.Properties.PrivateEndpointConnections, &armstorage.PrivateEndpointConnection{
							Properties: &armstorage.PrivateEndpointConnectionProperties{
								PrivateLinkServiceConnectionState: &armstorage.PrivateLinkServiceConnectionState{
									Status: new(armstorage.PrivateEndpointServiceConnectionStatusRejected),
								},
							},
						})
				case "failed endpoint":
					f.bodies[pe].(map[string]any)["properties"].(map[string]any)["provisioningState"] = "Failed"
				case "wrong DNS link":
					f.bodies[zone+"/virtualNetworkLinks"] = map[string]any{"value": []any{}}
				case "direct peering", "one-sided peering":
					remote := strings.Replace(resourceValue(f.base.vnet.ID), "/aks", "/remote", 1)
					f.bodies[pe].(map[string]any)["properties"].(map[string]any)["subnet"] =
						map[string]any{"id": remote + "/subnets/pe"}
					f.base.vnet.Properties.VirtualNetworkPeerings = []*armnetwork.VirtualNetworkPeering{{
						Properties: &armnetwork.VirtualNetworkPeeringPropertiesFormat{
							RemoteVirtualNetwork: &armnetwork.SubResource{ID: new(remote)},
							PeeringState:         new(armnetwork.VirtualNetworkPeeringStateConnected),
						},
					}}
					f.bodies[remote] = map[string]any{"id": remote, "properties": map[string]any{
						"provisioningState": "Succeeded", "virtualNetworkPeerings": []any{map[string]any{
							"properties": map[string]any{"peeringState": "Connected",
								"remoteVirtualNetwork": map[string]any{"id": resourceValue(f.base.vnet.ID)}},
						}},
					}}
					if scenario == "one-sided peering" {
						f.bodies[remote] = map[string]any{"id": remote, "properties": map[string]any{
							"provisioningState": "Succeeded", "virtualNetworkPeerings": []any{},
						}}
					}
				}
			}
			checker := f.checker()
			var report Report
			pipeline := Pipeline{
				Resources: contractResources(func(ctx context.Context, plan Plan) (Facts, Report, error) {
					report = checker.networkChecks(ctx, plan, &f.base.fixture.Cluster, f.base.fixture.Pools, f.account)
					return Facts{}, report, report.Err()
				}),
				Access: contractAccess(func(context.Context, Plan, Facts) (Report, error) { return nil, nil }),
			}
			_, _, err := pipeline.Preflight(t.Context(), f.base.plan)
			check := resourceRuleContractCheck(t, report, "S03")
			assert.Equal(t, expected, check.Status)
			require.NoError(t, err)
			require.NoError(t, report.Err())
			if expected == NotApplicable && scenario != "new" {
				assert.False(t, check.Mandatory)
				assert.NotEmpty(t, check.Warning)
				var output bytes.Buffer
				require.NoError(t, report.Write(&output))
				assert.Contains(t, output.String(), "S03 NOT_APPLICABLE")
				assert.Contains(t, output.String(), "WARNING: S03")
			}
			assert.Empty(t, f.reads, "S03 does not read agent pools, network resources or topology")
		})
	}
}

func TestStorageSkippedNetworkReadFailuresDoNotBlock(t *testing.T) {
	for _, status := range []int{403, 404} {
		for _, resource := range []string{"subnet", "endpoint", "zone", "links"} {
			t.Run(resource+http.StatusText(status), func(t *testing.T) {
				f := newStorageNetworkContract(t)
				pe, zone := f.privatePath()
				path := map[string]string{
					"subnet":   resourceValue(f.base.fixture.Pools[1].Properties.VnetSubnetID),
					"endpoint": pe, "zone": zone, "links": zone + "/virtualNetworkLinks",
				}[resource]
				f.errors[path] = status
				report := f.checker().networkChecks(
					t.Context(), f.base.plan, &f.base.fixture.Cluster, f.base.fixture.Pools, f.account)
				require.NoError(t, report.Err())
				check := resourceRuleContractCheck(t, report, "S03")
				assert.Equal(t, NotApplicable, check.Status)
				assert.False(t, check.Mandatory)
				assert.Empty(t, f.reads, "skipped network evidence is never requested")
			})
		}
	}
}

func TestStorageBasicPropertiesRemainMandatory(t *testing.T) {
	for _, network := range []string{"public", "restricted", "private"} {
		t.Run(network, func(t *testing.T) {
			for _, property := range []string{
				"account", "properties", "endpoint", "empty endpoint", "insecure endpoint", "malformed endpoint",
				"access", "unknown access", "firewall", "default action", "unknown default action",
			} {
				t.Run(property, func(t *testing.T) {
					f := newStorageNetworkContract(t)
					if network == "restricted" {
						f.account.Properties.NetworkRuleSet.DefaultAction = new(armstorage.DefaultActionDeny)
					} else if network == "private" {
						f.privatePath()
					}
					switch property {
					case "account":
						f.account = nil
					case "properties":
						f.account.Properties = nil
					case "endpoint":
						f.account.Properties.PrimaryEndpoints = nil
					case "empty endpoint":
						f.account.Properties.PrimaryEndpoints.Blob = nil
					case "insecure endpoint":
						f.account.Properties.PrimaryEndpoints.Blob = new("http://customer.blob.core.windows.net/")
					case "malformed endpoint":
						f.account.Properties.PrimaryEndpoints.Blob = new("https://%zz")
					case "access":
						f.account.Properties.PublicNetworkAccess = nil
					case "unknown access":
						f.account.Properties.PublicNetworkAccess = new(armstorage.PublicNetworkAccess("unknown"))
					case "firewall":
						f.account.Properties.NetworkRuleSet = nil
					case "default action":
						f.account.Properties.NetworkRuleSet.DefaultAction = nil
					case "unknown default action":
						f.account.Properties.NetworkRuleSet.DefaultAction = new(armstorage.DefaultAction("unknown"))
					}
					report := storageNetworkChecks(f.base.plan, f.account)
					require.Error(t, report.Err())
					assert.Equal(t, Unverified, report[0].Status)
					assert.True(t, report[0].Mandatory)
				})
			}
		})
	}
}

func TestStorageWarningRedactsEndpointCredentials(t *testing.T) {
	f := newStorageNetworkContract(t)
	f.account.Properties.NetworkRuleSet.DefaultAction = new(armstorage.DefaultActionDeny)
	// #nosec G101 -- Synthetic credentials exercise diagnostic redaction.
	f.account.Properties.PrimaryEndpoints.Blob = new("https://user:password@customer.blob.core.windows.net/?sig=secret#private")
	report := storageNetworkChecks(f.base.plan, f.account)
	var output bytes.Buffer
	require.NoError(t, report.Write(&output))
	for _, credential := range []string{"user:", "password", "sig=", "secret", "#private"} {
		assert.NotContains(t, output.String(), credential)
	}
	assert.Contains(t, output.String(), "WARNING: S03")
}

func TestStorageActualResourceCheckerAndPipeline(t *testing.T) {
	for _, scenario := range []string{"new", "public", "complex", "forbidden", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			f := newStorageNetworkContract(t)
			var account armstorage.Account
			raw, err := json.Marshal(storageContractBody(f.base.plan.Storage.ID))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &account))
			account.Properties.PrimaryEndpoints = f.account.Properties.PrimaryEndpoints
			account.Properties.PublicNetworkAccess = f.account.Properties.PublicNetworkAccess
			account.Properties.NetworkRuleSet = f.account.Properties.NetworkRuleSet
			f.account = &account
			switch scenario {
			case "new":
				f.base.plan.Storage.Supplied = false
			case "complex":
				f.account.Properties.NetworkRuleSet.DefaultAction = new(armstorage.DefaultActionDeny)
			case "forbidden":
				f.errors[f.base.plan.Storage.ID] = 403
			case "missing":
				f.errors[f.base.plan.Storage.ID] = 404
			}
			pipeline := Pipeline{
				Resources: f.checker(),
				Access: contractAccess(func(context.Context, Plan, Facts) (Report, error) {
					return nil, nil
				}),
			}
			_, report, err := pipeline.Preflight(t.Context(), f.base.plan)
			switch scenario {
			case "forbidden", "missing":
				require.Error(t, err)
				cause, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, f.errors[f.base.plan.Storage.ID], cause.StatusCode)
			default:
				require.NoError(t, err)
				check := resourceRuleContractCheck(t, report, "S03")
				expected := map[string]Status{"new": NotApplicable, "public": Pass, "complex": NotApplicable}[scenario]
				assert.Equal(t, expected, check.Status)
				if scenario == "complex" {
					assert.False(t, check.Mandatory)
				}
			}
			if scenario == "new" {
				assert.NotContains(t, f.reads, f.base.plan.Storage.ID)
				for _, path := range f.reads {
					assert.NotContains(t, strings.ToLower(path), "microsoft.network")
				}
			}
		})
	}
}

func TestStoragePublicOnlyNeedsNoAgentPool(t *testing.T) {
	for _, scenario := range []string{"public", "restricted", "private", "private deny"} {
		t.Run(scenario, func(t *testing.T) {
			f := newStorageNetworkContract(t)
			expected := Pass
			switch scenario {
			case "restricted":
				f.account.Properties.NetworkRuleSet.DefaultAction = new(armstorage.DefaultActionDeny)
				expected = NotApplicable
			case "private", "private deny":
				f.privatePath()
				if scenario == "private deny" {
					f.account.Properties.NetworkRuleSet.DefaultAction = new(armstorage.DefaultActionDeny)
				}
				expected = NotApplicable
			}
			report := f.checker().networkChecks(t.Context(), f.base.plan, nil, nil, f.account)
			require.NoError(t, report.Err())
			check := resourceRuleContractCheck(t, report, "S03")
			assert.Equal(t, expected, check.Status)
			if expected == NotApplicable {
				assert.False(t, check.Mandatory)
				assert.NotEmpty(t, check.Warning)
			}
			assert.Empty(t, f.reads)
		})
	}
}

func TestStorageSkipDoesNotSkipBasicSubnetN02(t *testing.T) {
	f := newStorageNetworkContract(t)
	pe, _ := f.privatePath()
	f.errors[pe] = http.StatusForbidden
	agent := resourceValue(f.base.vnet.ID) + "/subnets/agents"
	f.base.plan.AgentSubnet = Resource{ID: agent, Supplied: true}
	f.errors[agent] = http.StatusForbidden
	f.bodies[agent] = map[string]any{"id": agent, "properties": map[string]any{
		"addressPrefix": "10.0.1.0/27", "provisioningState": "Succeeded",
		"delegations": []any{map[string]any{
			"properties": map[string]any{"serviceName": "Microsoft.App/environments"},
		}},
	}}
	report := f.checker().networkChecks(
		t.Context(), f.base.plan, &f.base.fixture.Cluster, f.base.fixture.Pools, f.account)
	check := resourceRuleContractCheck(t, report, "S03")
	assert.Equal(t, NotApplicable, check.Status)
	assert.False(t, check.Mandatory)
	err := report.Err()
	require.Error(t, err)
	cause, ok := errors.AsType[*azcore.ResponseError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusForbidden, cause.StatusCode)
	blocked, ok := errors.AsType[*BlockedError](err)
	require.True(t, ok)
	for _, failure := range blocked.Checks {
		assert.Equal(t, "N02", failure.ID)
		assert.Equal(t, agent, failure.Scope)
	}
	assert.Contains(t, f.reads, agent, "S03 skipping does not suppress required N02 subnet reads")
	assert.NotContains(t, f.reads, pe, "Private Endpoint evidence is outside both checks")
}

func TestNewStorageObserveRetainsOwnershipOnly(t *testing.T) {
	f := newCleanupARM(t, false, false)
	f.deploy(t)
	base := f.options.Transport
	f.options.Transport = contractTransport(func(r *http.Request) (*http.Response, error) {
		path := strings.ToLower(r.URL.Path)
		assert.NotContains(t, path, "microsoft.network")
		if strings.Contains(path, "microsoft.storage") {
			storage := strings.ToLower(f.base.plan.Storage.ID)
			assert.Contains(t, []string{storage, storage + "/providers/microsoft.authorization/roleassignments"}, path)
		}
		return base.Do(r)
	})
	f.lifecycle = NewLifecycle(contractCredential{}, f.options, f.base.journal)
	require.NoError(t, f.lifecycle.Observe(t.Context(), f.base.plan))
}
