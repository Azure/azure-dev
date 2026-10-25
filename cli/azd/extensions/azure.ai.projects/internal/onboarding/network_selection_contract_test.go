// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type networkARMContract struct {
	t       *testing.T
	plan    Plan
	fixture resourceRuleContractFixture
	vnet    armnetwork.VirtualNetwork
	subnet  map[string]any
	pod     *armnetwork.Subnet
	methods []string
}

func newNetworkARMContract(t *testing.T) *networkARMContract {
	t.Helper()
	input := contractPlanInput()
	input.AgentSubnetID = ""
	input.Location = "westus"
	plan, err := BuildPlan(input)
	require.NoError(t, err)
	fixture := newResourceRuleContractFixture(t)
	fixture.Cluster.ID = new(plan.Cluster.ID)
	fixture.Cluster.Properties.NetworkProfile.ServiceCidr = new("10.3.0.0/16")
	fixture.Cluster.Properties.NetworkProfile.PodCidr = new("10.244.0.0/16")
	const vnet = "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/virtualNetworks/aks"
	fixture.Pools[1].Properties.VnetSubnetID = new(vnet + "/subnets/nodes")
	fixture.Pools[1].ID = new(plan.Cluster.ID + "/agentPools/agents")
	return &networkARMContract{
		t: t, plan: plan, fixture: fixture,
		vnet: armnetwork.VirtualNetwork{ID: new(vnet), Location: new("westus"),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
				AddressSpace:      &armnetwork.AddressSpace{AddressPrefixes: []*string{new("10.0.0.0/16")}},
				Subnets: []*armnetwork.Subnet{{ID: new(vnet + "/subnets/nodes"), Name: new("nodes"),
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: new("10.0.0.0/24"), ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
					}}},
			}},
	}
}

func (f *networkARMContract) options() *arm.ClientOptions {
	return contractARMOptions(func(request *http.Request) (*http.Response, error) {
		f.methods = append(f.methods, request.Method+" "+request.URL.Path)
		require.Equal(f.t, http.MethodGet, request.Method, "hosting subnet selection must never create network resources")
		var body any
		switch {
		case strings.HasSuffix(request.URL.Path, "/agentPools"):
			body = map[string]any{"value": f.fixture.Pools}
		case strings.Contains(strings.ToLower(request.URL.Path), "/providers/microsoft.compute/skus"):
			body = map[string]any{"value": f.fixture.SKUs}
		case strings.EqualFold(request.URL.Path, f.plan.Cluster.ID):
			body = f.fixture.Cluster
		case strings.EqualFold(request.URL.Path, resourceValue(f.vnet.ID)):
			body = f.vnet
		case strings.EqualFold(request.URL.Path, resourceValue(f.fixture.Pools[1].Properties.VnetSubnetID)):
			body = f.vnet.Properties.Subnets[0]
		case f.pod != nil && strings.EqualFold(request.URL.Path, resourceValue(f.pod.ID)):
			body = f.pod
		case f.subnet != nil && strings.EqualFold(request.URL.Path, f.subnet["id"].(string)):
			body = f.subnet
		default:
			f.t.Fatalf("unexpected network request: %s %s", request.Method, request.URL.Path)
		}
		data, err := json.Marshal(body)
		require.NoError(f.t, err)
		return contractARMResponse(request, 200, string(data)), nil
	})
}

func addExternalNetworkTopology(f *networkARMContract) {
	parent := resourceValue(f.vnet.ID)
	for _, name := range []string{"GatewaySubnet", "RouteServerSubnet", "AzureFirewallSubnet"} {
		f.vnet.Properties.Subnets = append(f.vnet.Properties.Subnets, &armnetwork.Subnet{
			ID: new(parent + "/subnets/" + name), Name: new(name),
			Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: new("10.0.1.0/27")},
		})
	}
	f.vnet.Properties.VirtualNetworkPeerings = []*armnetwork.VirtualNetworkPeering{{
		Properties: &armnetwork.VirtualNetworkPeeringPropertiesFormat{
			PeeringState:        new(armnetwork.VirtualNetworkPeeringStateConnected),
			AllowGatewayTransit: new(true), UseRemoteGateways: new(true),
			RemoteAddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{new("10.0.1.0/27")}},
		},
	}}
}

func TestStandardNetworkSelectionLimitsN02ToExistingAgentSubnet(t *testing.T) {
	for _, scenario := range []string{"none", "managed", "reference", "create"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNetworkARMContract(t)
			input := contractPlanInput()
			input.StorageID, input.RegistryID, input.AgentSubnetID = "", "", ""
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			selection := DeploymentSelection{}
			if scenario != "none" {
				selection.Network = &LegacyNetwork{
					Enabled: true, ManagedEgress: scenario == "managed",
					VNetID: resourceValue(f.vnet.ID), AgentSubnetName: "selected", PESubnetName: "pe",
					CreateAgentSubnet: scenario == "create",
				}
			}
			plan, err = WithDeploymentOperations(plan, selection)
			require.NoError(t, err)
			if scenario == "reference" {
				f.subnet = map[string]any{"id": plan.AgentSubnet.ID, "properties": map[string]any{
					"addressPrefix": "10.0.1.0/27", "provisioningState": "Succeeded",
					"delegations": []any{map[string]any{
						"properties": map[string]any{"serviceName": "Microsoft.App/environments"},
					}},
				}}
			}
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(f.options())}
			report := checker.networkChecks(t.Context(), plan, &f.fixture.Cluster, f.fixture.Pools, nil)
			var n02 Report
			for _, check := range report {
				if check.ID == "N02" {
					n02 = append(n02, check)
				}
			}
			require.NotEmpty(t, n02)
			if scenario == "reference" {
				require.NoError(t, n02.Err(), "N02 validates the existing standard Agent subnet")
			} else {
				require.Len(t, n02, 1)
				assert.Equal(t, NotApplicable, n02[0].Status)
				assert.Empty(t, f.methods, "N02 reads only existing customer Agent-subnet selections")
			}
		})
	}
}

func TestHostingSubnetN02UsesOnlySelectedNodeCIDR(t *testing.T) {
	for _, scenario := range []string{
		"valid", "Pod Service sibling peer overlap", "Pod subnet overlap", "missing Pod Service sibling peer metadata",
		"node overlap", "missing node CIDR", "unreadable node", "missing delegation",
		"disconnected peering", "missing peering state", "unreadable VNet",
		"too small", "public CIDR", "outside parent", "recommended size", "IPv6",
		"missing subnet CIDR", "noncanonical subnet CIDR", "missing subnet properties",
		"missing parent address space", "missing parent prefixes", "failed parent state", "missing parent state",
		"failed node state", "missing node state", "wrong node ID", "unreadable agent", "missing agent", "missing node",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newNetworkARMContract(t)
			addExternalNetworkTopology(f)
			input := contractPlanInput()
			input.StorageID, input.RegistryID = "", ""
			input.AgentSubnetID = resourceValue(f.vnet.ID) + "/subnets/supplied"
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			properties := map[string]any{
				"addressPrefix": "10.0.1.0/27", "provisioningState": "Succeeded",
				"delegations": []any{map[string]any{"properties": map[string]any{"serviceName": "Microsoft.App/environments"}}},
			}
			f.subnet = map[string]any{"id": plan.AgentSubnet.ID, "properties": properties}
			switch scenario {
			case "Pod Service sibling peer overlap":
				f.fixture.Cluster.Properties.NetworkProfile.PodCidr = new("10.0.1.0/27")
				f.fixture.Cluster.Properties.NetworkProfile.ServiceCidr = new("10.0.1.0/27")
			case "Pod subnet overlap":
				f.fixture.Pools[1].Properties.PodSubnetID = new(resourceValue(f.vnet.ID) + "/subnets/pods")
				f.pod = &armnetwork.Subnet{ID: f.fixture.Pools[1].Properties.PodSubnetID,
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: new("10.0.1.0/27"), ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
					}}
			case "missing Pod Service sibling peer metadata":
				f.fixture.Cluster.Properties.NetworkProfile.PodCidr = nil
				f.fixture.Cluster.Properties.NetworkProfile.ServiceCidr = nil
				f.vnet.Properties.Subnets[1].Properties.AddressPrefix = nil
				f.vnet.Properties.VirtualNetworkPeerings[0].Properties.RemoteAddressSpace = nil
			case "node overlap":
				properties["addressPrefix"] = "10.0.0.0/27"
			case "missing node CIDR":
				f.vnet.Properties.Subnets[0].Properties.AddressPrefix = nil
			case "missing delegation":
				delete(properties, "delegations")
			case "too small":
				properties["addressPrefix"] = "10.0.1.0/28"
			case "public CIDR":
				properties["addressPrefix"] = "8.8.8.0/27"
			case "outside parent":
				properties["addressPrefix"] = "10.9.0.0/27"
			case "recommended size":
				properties["addressPrefix"] = "10.0.1.0/24"
			case "IPv6":
				properties["addressPrefix"] = "fd00::/64"
			case "missing subnet CIDR":
				delete(properties, "addressPrefix")
			case "noncanonical subnet CIDR":
				properties["addressPrefix"] = "10.0.1.1/27"
			case "missing subnet properties":
				f.subnet["properties"] = nil
			case "missing parent address space":
				f.vnet.Properties.AddressSpace = nil
			case "missing parent prefixes":
				f.vnet.Properties.AddressSpace.AddressPrefixes = nil
			case "failed parent state":
				f.vnet.Properties.ProvisioningState = new(armnetwork.ProvisioningStateFailed)
			case "missing parent state":
				f.vnet.Properties.ProvisioningState = nil
			case "failed node state":
				f.vnet.Properties.Subnets[0].Properties.ProvisioningState = new(armnetwork.ProvisioningStateFailed)
			case "missing node state":
				f.vnet.Properties.Subnets[0].Properties.ProvisioningState = nil
			case "wrong node ID":
				f.vnet.Properties.Subnets[0].ID = new(resourceValue(f.vnet.ID) + "/subnets/different")
			case "disconnected peering":
				f.vnet.Properties.VirtualNetworkPeerings[0].Properties.PeeringState =
					new(armnetwork.VirtualNetworkPeeringStateDisconnected)
			case "missing peering state":
				f.vnet.Properties.VirtualNetworkPeerings[0].Properties.PeeringState = nil
			}

			base := f.options()
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				if scenario == "unreadable VNet" && strings.EqualFold(request.URL.Path, resourceValue(f.vnet.ID)) ||
					scenario == "unreadable node" &&
						strings.EqualFold(request.URL.Path, resourceValue(f.fixture.Pools[1].Properties.VnetSubnetID)) ||
					scenario == "unreadable agent" && strings.EqualFold(request.URL.Path, plan.AgentSubnet.ID) {
					return contractARMResponse(request, 403, `{"error":{"code":"AuthorizationFailed"}}`), nil
				}
				if scenario == "missing agent" && strings.EqualFold(request.URL.Path, plan.AgentSubnet.ID) ||
					scenario == "missing node" &&
						strings.EqualFold(request.URL.Path, resourceValue(f.fixture.Pools[1].Properties.VnetSubnetID)) {
					return contractARMResponse(request, http.StatusNotFound, `{"error":{"code":"ResourceNotFound"}}`), nil
				}
				return base.Transport.Do(request)
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
			report := checker.networkChecks(t.Context(), plan, &f.fixture.Cluster, f.fixture.Pools, nil)
			var n02 Report
			for _, check := range report {
				assert.NotEqual(t, "N01", check.ID)
				if check.ID == "N02" {
					n02 = append(n02, check)
				}
			}
			require.NotEmpty(t, n02)
			switch scenario {
			case "valid", "Pod Service sibling peer overlap", "Pod subnet overlap", "missing Pod Service sibling peer metadata",
				"recommended size", "disconnected peering", "missing peering state":
				require.NoError(t, report.Err(), "only basic subnet conditions are required")
			default:
				require.Error(t, report.Err())
			}
			if scenario == "missing parent address space" || scenario == "missing parent prefixes" {
				check := n02[len(n02)-1]
				assert.Equal(t, plan.AgentSubnet.ID, check.Scope)
				assert.Equal(t, Unverified, check.Status, "absent parent CIDRs are missing evidence, not an observed conflict")
				assert.True(t, check.Mandatory)
			}
			if scenario == "unreadable VNet" || scenario == "unreadable node" || scenario == "unreadable agent" ||
				scenario == "missing agent" || scenario == "missing node" {
				cause, ok := errors.AsType[*azcore.ResponseError](report.Err())
				require.True(t, ok)
				expectedStatus := http.StatusForbidden
				if strings.HasPrefix(scenario, "missing ") {
					expectedStatus = http.StatusNotFound
				}
				assert.Equal(t, expectedStatus, cause.StatusCode)
			}
			if scenario == "Pod subnet overlap" {
				assert.NotContains(t, f.methods, "GET "+resourceValue(f.pod.ID),
					"N02-only checks must not read Pod subnets")
			}
		})
	}
}

func TestN02BasicSubnetIgnoresAssociatedTopology(t *testing.T) {
	for _, network := range []string{"public", "restricted", "private"} {
		t.Run(network, func(t *testing.T) {
			f := newStorageNetworkContract(t)
			pe, _ := f.privatePath()
			if network != "private" {
				f.account.Properties.PublicNetworkAccess = new(armstorage.PublicNetworkAccessEnabled)
			}
			if network == "restricted" {
				f.account.Properties.NetworkRuleSet.DefaultAction = new(armstorage.DefaultActionDeny)
			}
			f.account.Properties.NetworkRuleSet.IPRules = []*armstorage.IPRule{{IPAddressOrRange: new("invalid")}}
			f.account.Properties.NetworkRuleSet.VirtualNetworkRules = []*armstorage.VirtualNetworkRule{{}}
			f.account.Properties.PrivateEndpointConnections[0].Properties.PrivateLinkServiceConnectionState.Status =
				new(armstorage.PrivateEndpointServiceConnectionStatusPending)
			agent := resourceValue(f.base.vnet.ID) + "/subnets/agents"
			nsg := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/networkSecurityGroups/agents"
			route := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/routeTables/agents"
			f.base.plan.AgentSubnet = Resource{ID: agent, Supplied: true}
			f.bodies[agent] = map[string]any{"id": agent, "properties": map[string]any{
				"addressPrefix": "10.0.1.0/27",
				"delegations": []any{map[string]any{
					"properties": map[string]any{"serviceName": "Microsoft.App/environments"},
				}},
				"networkSecurityGroup": map[string]any{"id": nsg}, "routeTable": map[string]any{"id": route},
			}}
			node := f.base.vnet.Properties.Subnets[0]
			node.Properties.NetworkSecurityGroup = &armnetwork.SecurityGroup{ID: new(nsg)}
			node.Properties.RouteTable = &armnetwork.RouteTable{ID: new(route)}
			addExternalNetworkTopology(f.base)
			f.base.vnet.Properties.VirtualNetworkPeerings[0].Properties.PeeringState =
				new(armnetwork.VirtualNetworkPeeringStateDisconnected)
			f.base.vnet.Properties.DhcpOptions = &armnetwork.DhcpOptions{DNSServers: []*string{nil, new("invalid")}}
			for _, path := range []string{pe, nsg, route} {
				f.errors[path] = http.StatusForbidden
			}
			report := f.checker().networkChecks(
				t.Context(), f.base.plan, &f.base.fixture.Cluster, f.base.fixture.Pools, f.account)
			require.NoError(t, report.Err())
			expected := NotApplicable
			if network == "public" {
				expected = Pass
			}
			assert.Equal(t, expected, resourceRuleContractCheck(t, report, "S03").Status)
			var basicChecks Report
			for _, check := range report {
				if check.ID == "N02" {
					basicChecks = append(basicChecks, check)
					assert.Equal(t, Pass, check.Status, check.Observed)
				}
			}
			require.NotEmpty(t, basicChecks)
			var networkReads []string
			for _, path := range f.reads {
				if strings.Contains(strings.ToLower(path), "microsoft.network") {
					networkReads = append(networkReads, path)
				}
			}
			assert.ElementsMatch(t, []string{agent, resourceValue(f.base.vnet.ID), resourceValue(node.ID)}, networkReads)
		})
	}
}

func TestOmittedHostingSubnetPlanNeverOwnsNetworkResources(t *testing.T) {
	for _, legacy := range []string{"none", "managed", "existing agent", "created agent"} {
		t.Run(legacy, func(t *testing.T) {
			f, facts := preparationContractFixture(t, true, true, false)
			input := contractPlanInput()
			input.AgentSubnetID = ""
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			f.plan = plan
			selection := DeploymentSelection{}
			if legacy != "none" {
				selection.Network = &LegacyNetwork{
					Enabled: true, VNetID: resourceValue(newNetworkARMContract(t).vnet.ID),
					AgentSubnetName: "agents", PESubnetName: "pe",
					ManagedEgress: legacy == "managed", CreateAgentSubnet: legacy == "created agent",
					CreatePESubnet: true,
				}
			}
			f.plan, err = WithDeploymentOperations(plan, selection)
			require.NoError(t, err)
			baseline := slices.ContainsFunc(f.plan.Operations, func(op Operation) bool {
				return op.CheckID == "P03" && op.Action == "Microsoft.Network/virtualNetworks/write"
			})
			require.True(t, baseline, "the independent guide baseline is not an N02 exemption")
			if legacy == "none" {
				for _, op := range f.plan.Operations {
					assert.False(t, strings.Contains(op.Action, "virtualNetworks/subnets"),
						"omission adds no hosting subnet read/write/join requests")
				}
			}
			planned, err := PlannedDeployment(f.plan, facts)
			require.NoError(t, err)
			resumed, err := PlannedDeployment(f.plan, facts)
			require.NoError(t, err)
			assert.Equal(t, planned, resumed)
			assert.Empty(t, f.writes, "planning never writes Azure")
			state, err := f.journal.Load(t.Context())
			require.NoError(t, err)
			assert.Nil(t, state.Network)
			assert.NotContains(t, state.Resources, "R06Subnet")
			assert.Empty(t, f.journal.data, "planning never persists a preparation journal")
		})
	}
}

func TestNetworkRetryRejectsOldDefaultsWithoutLosingCleanup(t *testing.T) {
	for _, scenario := range []string{"old default", "old incomplete default"} {
		t.Run(scenario, func(t *testing.T) {
			input := contractPlanInput()
			input.AgentSubnetID = ""
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			journal := &memoryPreparationJournal{}
			state := PreparationState{Version: preparationJournalVersion, Owner: plan.Owner,
				SubscriptionID: plan.SubscriptionID, ResourceGroup: plan.ResourceGroup, Location: plan.Location,
				Resources: map[string]OwnedResource{}}
			switch scenario {
			case "old default", "old incomplete default":
				vnet := "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/virtualNetworks/aks"
				state.Network = &SubnetPreparation{VNetID: vnet, SubnetID: vnet + "/subnets/old-owned",
					AddressPrefix: "10.0.1.0/27", Location: plan.Location}
				state.Resources["R06Subnet"] = OwnedResource{
					ID: state.Network.SubnetID, Complete: scenario == "old default",
				}
			}
			require.NoError(t, journal.Save(t.Context(), state))
			before := slices.Clone(journal.data)
			err = validateHistoricalNetwork(state)
			require.Error(t, err)
			assert.Equal(t, before, journal.data, "failed retry must preserve every cleanup record")
			if strings.HasPrefix(scenario, "old ") {
				_, err := CleanupPlan(state)
				require.NoError(t, err, "old owned-subnet cleanup stays supported")
			}
			l := &ownedLifecycle{journal: journal}
			account := PlannedFoundryAccount(plan).ID
			spec := FoundrySpec{
				AccountID: account, ProjectID: account + "/projects/project",
				RegistryID: plan.Registry.ID, Location: plan.Location,
			}
			_, err = l.Check(t.Context(), plan, spec)
			require.Error(t, err, "historical owned network blocks retry before lifecycle reads or preparation writes")
			assert.Equal(t, before, journal.data)
		})
	}
}

func TestUnreferencedHostingSubnetSkipsN02ButKeepsIndependentNetworkChecks(t *testing.T) {
	for _, selection := range []string{"omitted", "create"} {
		t.Run(selection, func(t *testing.T) {
			f := newNetworkARMContract(t)
			plan := f.plan
			if selection == "create" {
				var err error
				plan, err = WithDeploymentOperations(plan, DeploymentSelection{Network: &LegacyNetwork{
					Enabled: true, VNetID: resourceValue(f.vnet.ID),
					AgentSubnetName: "new-agent", PESubnetName: "pe", CreateAgentSubnet: true,
				}})
				require.NoError(t, err)
			}
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(f.options())}
			storage := &armstorage.Account{ID: new(plan.Storage.ID), Properties: &armstorage.AccountProperties{
				PublicNetworkAccess: new(armstorage.PublicNetworkAccessEnabled),
				NetworkRuleSet:      &armstorage.NetworkRuleSet{DefaultAction: new(armstorage.DefaultActionAllow)},
				PrimaryEndpoints:    &armstorage.Endpoints{Blob: new("https://storage.blob.core.windows.net/")},
			}}
			report := checker.networkChecks(t.Context(), plan, &f.fixture.Cluster, f.fixture.Pools, storage)
			n02 := 0
			for _, check := range report {
				if check.ID == "N02" {
					n02++
					assert.Equal(t, NotApplicable, check.Status)
				}
			}
			assert.Equal(t, 1, n02)
			assert.Equal(t, Pass, resourceRuleContractCheck(t, report, "S03").Status)
			for _, read := range f.methods {
				assert.NotEqual(t, "GET "+resourceValue(f.vnet.ID), read, "skipped N02 never reads the Agent VNet")
				assert.NotContains(t, read, "/subnets/new-agent")
			}
		})
	}
}
