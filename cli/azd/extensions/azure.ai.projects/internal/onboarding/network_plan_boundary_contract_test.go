// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const networkBoundaryVNetID = "/subscriptions/customer/resourceGroups/network/providers/" +
	"Microsoft.Network/virtualNetworks/customer"

func networkBoundaryAddressSpace(prefixes ...string) *armnetwork.AddressSpace {
	space := &armnetwork.AddressSpace{}
	for _, prefix := range prefixes {
		space.AddressPrefixes = append(space.AddressPrefixes, new(prefix))
	}
	return space
}

func networkBoundaryVNet(prefixes ...string) *armnetwork.VirtualNetwork {
	return &armnetwork.VirtualNetwork{
		ID: new(networkBoundaryVNetID), Location: new("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace:      networkBoundaryAddressSpace(prefixes...),
			ProvisioningState: new(armnetwork.ProvisioningState("Succeeded")),
		},
	}
}

func networkBoundarySubnet(name, prefix string) *armnetwork.Subnet {
	return &armnetwork.Subnet{
		ID: new(networkBoundaryVNetID + "/subnets/" + name), Name: new(name),
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: new(prefix)},
	}
}

func networkBoundaryCluster() armcontainerservice.ManagedCluster {
	return armcontainerservice.ManagedCluster{
		Properties: &armcontainerservice.ManagedClusterProperties{
			NetworkProfile: &armcontainerservice.NetworkProfile{
				NetworkPlugin:     new(armcontainerservice.NetworkPlugin("azure")),
				NetworkPluginMode: new(armcontainerservice.NetworkPluginMode("overlay")),
				PodCidr:           new("172.20.0.0/16"),
				ServiceCidr:       new("10.96.0.0/16"),
			},
		},
	}
}

func networkBoundaryPeering() *armnetwork.VirtualNetworkPeering {
	return &armnetwork.VirtualNetworkPeering{
		Properties: &armnetwork.VirtualNetworkPeeringPropertiesFormat{
			PeeringState:                     new(armnetwork.VirtualNetworkPeeringState("Connected")),
			PeeringSyncLevel:                 new(armnetwork.VirtualNetworkPeeringLevel("FullyInSync")),
			ProvisioningState:                new(armnetwork.ProvisioningState("Succeeded")),
			RemoteAddressSpace:               networkBoundaryAddressSpace("10.7.0.0/16"),
			RemoteVirtualNetworkAddressSpace: networkBoundaryAddressSpace("10.7.0.0/16"),
			AllowGatewayTransit:              new(false),
			UseRemoteGateways:                new(false),
		},
	}
}

func TestNetworkPlanBoundaryAllocatesOnlyUnusedPrivateIPv4(t *testing.T) {
	for _, tt := range []struct {
		name     string
		spaces   []string
		occupied []string
		want     string
	}{
		{name: "empty private space", spaces: []string{"10.2.0.0/24"}, want: "10.2.0.0/27"},
		{name: "exact minimum fit", spaces: []string{"10.2.0.0/27"}, want: "10.2.0.0/27"},
		{name: "too small", spaces: []string{"10.2.0.0/28"}},
		{name: "IPv6 only", spaces: []string{"fd00::/56"}},
		{name: "IPv4 mapped IPv6", spaces: []string{"::ffff:10.2.0.0/120"}},
		{name: "public only", spaces: []string{"8.8.8.0/24"}},
		{name: "empty inventory"},
		{name: "invalid prefix", spaces: []string{"invalid"}},
		{name: "noncanonical prefix", spaces: []string{"10.2.0.1/24"}},
		{
			name: "unknown secondary space", spaces: []string{"10.2.0.0/24", "invalid"},
		},
		{
			name: "one occupied host", spaces: []string{"10.2.0.0/26"},
			occupied: []string{"10.2.0.1/32"}, want: "10.2.0.32/27",
		},
		{
			name: "occupied smaller subnet", spaces: []string{"10.2.0.0/26"},
			occupied: []string{"10.2.0.16/28"}, want: "10.2.0.32/27",
		},
		{
			name: "exhausted", spaces: []string{"10.2.0.0/26"},
			occupied: []string{"10.2.0.0/27", "10.2.0.32/27"},
		},
		{
			name: "occupied supernet", spaces: []string{"10.2.0.0/24"},
			occupied: []string{"10.2.0.0/16"},
		},
		{
			name: "adjacent range is not overlap", spaces: []string{"10.2.0.0/24"},
			occupied: []string{"10.2.1.0/24"}, want: "10.2.0.0/27",
		},
		{
			name: "IPv6 occupancy is separate", spaces: []string{"10.2.0.0/24"},
			occupied: []string{"fd00::/64"}, want: "10.2.0.0/27",
		},
		{
			name: "stable sorted choice", spaces: []string{"10.9.0.0/24", "10.2.0.0/24"},
			want: "10.2.0.0/27",
		},
		{
			name: "continue after exhausted space", spaces: []string{"10.2.0.0/27", "10.3.0.0/27"},
			occupied: []string{"10.2.0.0/27"}, want: "10.3.0.0/27",
		},
		{
			name: "skip large occupied block", spaces: []string{"172.16.0.0/12"},
			occupied: []string{"172.16.0.0/13"}, want: "172.24.0.0/27",
		},
		{
			name: "last private subnet exhausted", spaces: []string{"192.168.255.224/27"},
			occupied: []string{"192.168.255.224/27"},
		},
		{
			name: "do not cross into public 11 space", spaces: []string{"10.0.0.0/7"},
			occupied: []string{"10.0.0.0/8"},
		},
		{
			name: "do not cross into public 192 space", spaces: []string{"192.168.0.0/15"},
			occupied: []string{"192.168.0.0/16"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parent := networkBoundaryVNet(tt.spaces...)
			var occupied []netip.Prefix
			for _, raw := range tt.occupied {
				occupied = append(occupied, netip.MustParsePrefix(raw))
			}
			prefix, err := allocateAgentPrefix(parent, occupied)
			if tt.want == "" {
				require.Error(t, err)
				assert.False(t, prefix.IsValid())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, netip.MustParsePrefix(tt.want), prefix)
			assert.True(t, prefix.Addr().Is4())
			assert.True(t, prefix.Addr().IsPrivate())
			assert.Equal(t, 27, prefix.Bits())
			for _, other := range occupied {
				assert.False(t, prefix.Overlaps(other), "selected range overlaps %s", other)
			}
		})
	}
}

func TestNetworkPlanBoundaryAbsentAddressMetadata(t *testing.T) {
	for _, parent := range []*armnetwork.VirtualNetwork{
		nil,
		{},
		{Properties: &armnetwork.VirtualNetworkPropertiesFormat{}},
	} {
		var prefix netip.Prefix
		var err error
		require.NotPanics(t, func() { prefix, err = allocateAgentPrefix(parent, nil) })
		require.Error(t, err)
		assert.False(t, prefix.IsValid())
	}
}

func TestNetworkPlanBoundaryInventoriesAllAddressForms(t *testing.T) {
	parent := networkBoundaryVNet("10.2.0.0/16")
	parent.Properties.Subnets = []*armnetwork.Subnet{networkBoundarySubnet("nodes", "10.2.0.0/24")}
	dual := networkBoundarySubnet("dual", "10.2.1.0/24")
	dual.Properties.AddressPrefixes = []*string{new("fd00:3::/64")}
	parent.Properties.Subnets = append(parent.Properties.Subnets, dual)
	parent.Properties.VirtualNetworkPeerings = []*armnetwork.VirtualNetworkPeering{networkBoundaryPeering()}
	cluster := networkBoundaryCluster()
	cluster.Properties.NetworkProfile.PodCidrs = []*string{new("172.20.0.0/16"), new("fd00:1::/64")}
	cluster.Properties.NetworkProfile.ServiceCidrs = []*string{new("10.96.0.0/16"), new("fd00:2::/108")}

	occupied, err := networkOccupiedPrefixes(cluster, parent)
	require.NoError(t, err)
	for _, prefix := range []string{
		"10.2.0.0/24", "10.2.1.0/24", "fd00:3::/64",
		"172.20.0.0/16", "fd00:1::/64", "10.96.0.0/16", "fd00:2::/108", "10.7.0.0/16",
	} {
		assert.Contains(t, occupied, netip.MustParsePrefix(prefix))
	}
}

func TestNetworkPlanBoundaryRejectsUnknownConnectedAddressEvidence(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*armnetwork.VirtualNetwork, *armcontainerservice.ManagedCluster)
	}{
		{
			name: "missing AKS properties",
			mutate: func(_ *armnetwork.VirtualNetwork, c *armcontainerservice.ManagedCluster) {
				c.Properties = nil
			},
		},
		{
			name: "missing profile",
			mutate: func(_ *armnetwork.VirtualNetwork, c *armcontainerservice.ManagedCluster) {
				c.Properties.NetworkProfile = nil
			},
		},
		{
			name: "missing Service ranges",
			mutate: func(_ *armnetwork.VirtualNetwork, c *armcontainerservice.ManagedCluster) {
				c.Properties.NetworkProfile.ServiceCidr = nil
				c.Properties.NetworkProfile.ServiceCidrs = nil
			},
		},
		{
			name: "missing Overlay Pod ranges",
			mutate: func(_ *armnetwork.VirtualNetwork, c *armcontainerservice.ManagedCluster) {
				c.Properties.NetworkProfile.PodCidr = nil
				c.Properties.NetworkProfile.PodCidrs = nil
			},
		},
		{
			name: "malformed Pod range",
			mutate: func(_ *armnetwork.VirtualNetwork, c *armcontainerservice.ManagedCluster) {
				c.Properties.NetworkProfile.PodCidr = new("invalid")
			},
		},
		{
			name: "unknown subnet",
			mutate: func(v *armnetwork.VirtualNetwork, _ *armcontainerservice.ManagedCluster) {
				v.Properties.Subnets = append(v.Properties.Subnets, nil)
			},
		},
		{
			name: "unknown subnet properties",
			mutate: func(v *armnetwork.VirtualNetwork, _ *armcontainerservice.ManagedCluster) {
				v.Properties.Subnets[0].Properties = nil
			},
		},
		{
			name: "unknown peer",
			mutate: func(v *armnetwork.VirtualNetwork, _ *armcontainerservice.ManagedCluster) {
				v.Properties.VirtualNetworkPeerings = []*armnetwork.VirtualNetworkPeering{nil}
			},
		},
		{
			name: "unknown peer properties",
			mutate: func(v *armnetwork.VirtualNetwork, _ *armcontainerservice.ManagedCluster) {
				v.Properties.VirtualNetworkPeerings[0].Properties = nil
			},
		},
		{
			name: "disconnected peer",
			mutate: func(v *armnetwork.VirtualNetwork, _ *armcontainerservice.ManagedCluster) {
				v.Properties.VirtualNetworkPeerings[0].Properties.PeeringState =
					new(armnetwork.VirtualNetworkPeeringState("Disconnected"))
			},
		},
		{
			name: "unknown remote space",
			mutate: func(v *armnetwork.VirtualNetwork, _ *armcontainerservice.ManagedCluster) {
				v.Properties.VirtualNetworkPeerings[0].Properties.RemoteAddressSpace = nil
			},
		},
		{
			name: "empty remote space",
			mutate: func(v *armnetwork.VirtualNetwork, _ *armcontainerservice.ManagedCluster) {
				v.Properties.VirtualNetworkPeerings[0].Properties.RemoteAddressSpace = networkBoundaryAddressSpace()
			},
		},
		{
			name: "non-canonical remote space",
			mutate: func(v *armnetwork.VirtualNetwork, _ *armcontainerservice.ManagedCluster) {
				v.Properties.VirtualNetworkPeerings[0].Properties.RemoteAddressSpace = networkBoundaryAddressSpace("10.7.0.1/16")
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parent := networkBoundaryVNet("10.2.0.0/16")
			parent.Properties.Subnets = []*armnetwork.Subnet{networkBoundarySubnet("nodes", "10.2.0.0/24")}
			parent.Properties.VirtualNetworkPeerings = []*armnetwork.VirtualNetworkPeering{networkBoundaryPeering()}
			cluster := networkBoundaryCluster()
			tt.mutate(parent, &cluster)
			_, err := networkOccupiedPrefixes(cluster, parent)
			require.Error(t, err, "unknown connected address evidence must not become an empty inventory")
		})
	}
}

func TestNetworkPlanBoundaryRetainsCurrentRemoteAddresses(t *testing.T) {
	parent := networkBoundaryVNet("10.2.0.0/16")
	parent.Properties.Subnets = []*armnetwork.Subnet{networkBoundarySubnet("nodes", "10.2.0.0/24")}
	peering := networkBoundaryPeering()
	peering.Properties.PeeringSyncLevel = new(armnetwork.VirtualNetworkPeeringLevel("RemoteNotInSync"))
	peering.Properties.RemoteVirtualNetworkAddressSpace = networkBoundaryAddressSpace("10.8.0.0/16")
	parent.Properties.VirtualNetworkPeerings = []*armnetwork.VirtualNetworkPeering{peering}
	occupied, err := networkOccupiedPrefixes(networkBoundaryCluster(), parent)
	if err != nil {
		return
	}
	for _, prefix := range []string{"10.7.0.0/16", "10.8.0.0/16"} {
		assert.Contains(t, occupied, netip.MustParsePrefix(prefix),
			"do not silently discard known current remote address space; retain it or reject unsupported sync state")
	}
}

func TestNetworkPlanBoundaryValidatesTrackedPrefixWithoutBorrowingOwnership(t *testing.T) {
	for _, tt := range []struct {
		name         string
		prefix       string
		subnets      []*armnetwork.Subnet
		allowTracked bool
		wantError    bool
	}{
		{name: "unused exact prefix", prefix: "10.2.1.0/27"},
		{name: "invalid prefix", prefix: "invalid", wantError: true},
		{name: "noncanonical prefix", prefix: "10.2.1.1/27", wantError: true},
		{name: "IPv6 prefix", prefix: "fd00::/27", wantError: true},
		{name: "public prefix", prefix: "8.8.8.0/27", wantError: true},
		{name: "outside parent", prefix: "10.3.1.0/27", wantError: true},
		{name: "unsupported larger default", prefix: "10.2.1.0/26", wantError: true},
		{name: "too small", prefix: "10.2.1.0/28", wantError: true},
		{
			name: "existing customer subnet", prefix: "10.2.1.0/27", wantError: true,
			subnets: []*armnetwork.Subnet{networkBoundarySubnet("customer", "10.2.1.0/27")},
		},
		{
			name: "same ID without ownership", prefix: "10.2.1.0/27", wantError: true,
			subnets: []*armnetwork.Subnet{networkBoundarySubnet("azd-agents-test", "10.2.1.0/27")},
		},
		{
			name: "exact tracked child", prefix: "10.2.1.0/27", allowTracked: true,
			subnets: []*armnetwork.Subnet{networkBoundarySubnet("azd-agents-test", "10.2.1.0/27")},
		},
		{
			name: "tracked child with changed address", prefix: "10.2.1.0/27", allowTracked: true, wantError: true,
			subnets: []*armnetwork.Subnet{networkBoundarySubnet("azd-agents-test", "10.2.1.32/27")},
		},
		{
			name: "other child never becomes owned", prefix: "10.2.1.0/27", allowTracked: true, wantError: true,
			subnets: []*armnetwork.Subnet{networkBoundarySubnet("customer", "10.2.1.0/27")},
		},
		{
			name: "another child also overlaps", prefix: "10.2.1.0/27", allowTracked: true, wantError: true,
			subnets: []*armnetwork.Subnet{
				networkBoundarySubnet("azd-agents-test", "10.2.1.0/27"),
				networkBoundarySubnet("customer", "10.2.1.0/28"),
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parent := networkBoundaryVNet("10.2.0.0/16")
			parent.Properties.Subnets = tt.subnets
			plan := SubnetPreparation{
				VNetID: networkBoundaryVNetID, SubnetID: networkBoundaryVNetID + "/subnets/azd-agents-test",
				AddressPrefix: tt.prefix, Location: "eastus",
			}
			err := validateDefaultPrefix(*parent, plan, tt.allowTracked)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNetworkPlanBoundaryHelpersPreserveSDKInput(t *testing.T) {
	parent := networkBoundaryVNet("10.2.0.0/16")
	tracked := networkBoundarySubnet("azd-agents-test", "10.2.1.0/27")
	tracked.ID = new(strings.ToUpper(*tracked.ID))
	parent.Properties.Subnets = []*armnetwork.Subnet{networkBoundarySubnet("nodes", "10.2.0.0/24"), tracked}
	parent.Properties.VirtualNetworkPeerings = []*armnetwork.VirtualNetworkPeering{networkBoundaryPeering()}
	cluster := networkBoundaryCluster()
	beforeParent, err := json.Marshal(parent)
	require.NoError(t, err)
	beforeCluster, err := json.Marshal(cluster)
	require.NoError(t, err)

	occupied, err := networkOccupiedPrefixes(cluster, parent)
	require.NoError(t, err)
	_, err = allocateAgentPrefix(parent, occupied)
	require.NoError(t, err)
	require.NoError(t, validateDefaultPrefix(*parent, SubnetPreparation{
		VNetID: networkBoundaryVNetID, SubnetID: networkBoundaryVNetID + "/subnets/azd-agents-test",
		AddressPrefix: "10.2.1.0/27", Location: "eastus",
	}, true))

	afterParent, err := json.Marshal(parent)
	require.NoError(t, err)
	afterCluster, err := json.Marshal(cluster)
	require.NoError(t, err)
	assert.JSONEq(t, string(beforeParent), string(afterParent))
	assert.JSONEq(t, string(beforeCluster), string(afterCluster))
}
