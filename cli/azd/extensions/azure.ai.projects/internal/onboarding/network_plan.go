// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
)

// ResolveDefaultNetwork selects an unused private /27 in the qualifying pool's existing VNet.
// Explicit subnet choices are preserved. Unsupported/opaque topology evidence remains a blocking error.
func ResolveDefaultNetwork(
	ctx context.Context, plan Plan, credential azcore.TokenCredential, options *arm.ClientOptions, journal Journal,
) (Plan, error) {
	if plan.AgentSubnet.Supplied || plan.NetworkChanges {
		return plan, nil
	}
	checker := &sdkResourceChecker{credential: credential, options: onboardingARMOptions(options)}
	cluster, pools, skus, report := checker.readCluster(ctx, plan.Cluster)
	if err := report.Err(); err != nil {
		return plan, fmt.Errorf("I03: resolve default network from AKS: %w", err)
	}
	if cluster == nil {
		return plan, fmt.Errorf("I03: default topology requires readable AKS metadata")
	}
	pool, checks := selectedAgentPool(*cluster, pools, skus)
	if pool == nil {
		return plan, fmt.Errorf("I03: no qualifying agent pool supplies a default VNet")
	}
	if err := checks.Err(); err != nil {
		return plan, fmt.Errorf("I03: default topology requires a complete qualifying agent pool: %w", err)
	}
	network := cluster.Properties.NetworkProfile
	if network == nil || resourceValue(network.NetworkPlugin) != "azure" ||
		resourceValue(network.NetworkPluginMode) != "overlay" && resourceValue(pool.Properties.PodSubnetID) == "" {
		return plan, fmt.Errorf("I03: the same qualifying pool requires Azure CNI Overlay or Pod Subnet")
	}
	source, err := existingResourceID(resourceValue(pool.Properties.VnetSubnetID),
		"Microsoft.Network/virtualNetworks/subnets")
	if err != nil {
		return plan, fmt.Errorf("I03: qualifying pool's node subnet is unavailable: %w", err)
	}
	if raw := resourceValue(pool.Properties.PodSubnetID); raw != "" {
		pod, err := existingResourceID(raw, "Microsoft.Network/virtualNetworks/subnets")
		if err != nil || !strings.EqualFold(pod.Parent.String(), source.Parent.String()) {
			return plan, fmt.Errorf("I03: default topology supports node and Pod subnets in the same VNet only")
		}
	}
	parent, checks := checker.readParentNetwork(ctx, source.String())
	if err := checks.Err(); err != nil {
		return plan, fmt.Errorf("I03: read default VNet: %w", err)
	}
	if parent == nil || parent.Properties == nil || parent.ID == nil ||
		!strings.EqualFold(*parent.ID, source.Parent.String()) ||
		!strings.EqualFold(resourceValue(parent.Location), plan.Location) ||
		resourceValue(parent.Properties.ProvisioningState) != "Succeeded" {
		return plan, fmt.Errorf("I03: default VNet must be ready, match the source ID and use the target region")
	}
	occupied, err := networkOccupiedPrefixes(*cluster, parent)
	if err != nil {
		return plan, fmt.Errorf("I03: default topology address evidence: %w", err)
	}
	name := "azd-agents-" + plan.Owner
	subnetID := source.Parent.String() + "/subnets/" + name
	var previous *SubnetPreparation
	if journal == nil {
		return plan, fmt.Errorf("I03: default topology requires a readable ownership journal")
	}
	if journal != nil {
		state, err := journal.Load(ctx)
		if err != nil {
			return plan, fmt.Errorf("I03: read default network recovery plan: %w", err)
		}
		if state.Owner != "" && state.Owner != plan.Owner {
			return plan, fmt.Errorf("I03: default network journal belongs to another environment")
		}
		previous = state.Network
		if state.CleanupComplete {
			previous = nil
		}
		if previous != nil &&
			(!strings.EqualFold(previous.VNetID, source.Parent.String()) ||
				!strings.EqualFold(previous.SubnetID, subnetID) || previous.Location != plan.Location) {
			return plan, fmt.Errorf("I03: recorded default topology differs from the qualifying pool's current VNet")
		}
		for _, subnet := range parent.Properties.Subnets {
			if subnet != nil && strings.EqualFold(resourceValue(subnet.ID), subnetID) {
				record, tracked := state.Resources["R06Subnet"]
				if previous == nil || !tracked || !strings.EqualFold(record.ID, subnetID) {
					return plan, fmt.Errorf("I03: default subnet name already exists without ownership; refusing adoption")
				}
			}
		}
	}
	if previous == nil {
		prefix, err := allocateAgentPrefix(parent, occupied)
		if err != nil {
			return plan, fmt.Errorf("I03: allocate default delegated subnet: %w", err)
		}
		plan.Network = &SubnetPreparation{
			VNetID: source.Parent.String(), SubnetID: subnetID, AddressPrefix: prefix.String(), Location: plan.Location,
		}
	} else {
		resume := *previous
		plan.Network = &resume
		if err := validateDefaultPrefix(*parent, resume, true); err != nil {
			return plan, fmt.Errorf("I03: recorded default subnet address is no longer valid: %w", err)
		}
		properties := *parent.Properties
		properties.Subnets = slices.DeleteFunc(slices.Clone(properties.Subnets), func(subnet *armnetwork.Subnet) bool {
			return subnet != nil && strings.EqualFold(resourceValue(subnet.ID), resume.SubnetID)
		})
		withoutOwned := *parent
		withoutOwned.Properties = &properties
		occupied, err := networkOccupiedPrefixes(*cluster, &withoutOwned)
		if err != nil {
			return plan, fmt.Errorf("I03: current default topology inventory is incomplete: %w", err)
		}
		prefix, _ := netip.ParsePrefix(resume.AddressPrefix)
		if slices.ContainsFunc(occupied, prefix.Overlaps) {
			return plan, fmt.Errorf("I03: recorded default subnet now overlaps AKS or connected address space")
		}
	}
	plan.AgentSubnet = Resource{ID: subnetID, Parent: source.Parent.String(), Name: name}
	plan.Operations = slices.Clone(plan.Operations)
	for i := range plan.Operations {
		op := &plan.Operations[i]
		if op.CheckID == "P03" && op.Action == "Microsoft.Network/virtualNetworks/write" {
			op.Scope = source.Parent.Parent.String()
		}
	}
	plan.Operations = append(plan.Operations,
		Operation{CheckID: "P08", Action: "Microsoft.Network/virtualNetworks/read", Scope: source.Parent.String()},
		Operation{CheckID: "P08", Action: "Microsoft.Network/virtualNetworks/subnets/read", Scope: source.Parent.String()},
		Operation{CheckID: "P09", Action: "Microsoft.Network/virtualNetworks/subnets/write", Scope: source.Parent.String()},
		Operation{CheckID: "P09", Action: "Microsoft.Network/virtualNetworks/subnets/join/action",
			Scope: source.Parent.String()},
	)
	return plan, nil
}

func networkOccupiedPrefixes(
	cluster armcontainerservice.ManagedCluster, parent *armnetwork.VirtualNetwork,
) ([]netip.Prefix, error) {
	occupied, err := networkParentPrefixes(parent)
	if err != nil {
		return nil, err
	}
	if cluster.Properties == nil || cluster.Properties.NetworkProfile == nil {
		return nil, fmt.Errorf("AKS Pod/Service address evidence is unavailable")
	}
	network := cluster.Properties.NetworkProfile
	if network.ServiceCidr == nil && len(network.ServiceCidrs) == 0 {
		return nil, fmt.Errorf("AKS Service CIDR is unavailable")
	}
	if resourceValue(network.NetworkPluginMode) == "overlay" &&
		network.PodCidr == nil && len(network.PodCidrs) == 0 {
		return nil, fmt.Errorf("AKS Overlay Pod CIDR is unavailable")
	}
	values := append(slices.Clone(network.PodCidrs), network.ServiceCidrs...)
	values = append(values, network.PodCidr, network.ServiceCidr)
	for _, raw := range values {
		if raw == nil {
			continue
		}
		prefix, err := netip.ParsePrefix(*raw)
		if err != nil {
			return nil, fmt.Errorf("AKS Pod/Service prefix is invalid: %w", err)
		}
		occupied = append(occupied, prefix)
	}
	return occupied, nil
}

func networkParentPrefixes(parent *armnetwork.VirtualNetwork) ([]netip.Prefix, error) {
	if parent == nil || parent.Properties == nil {
		return nil, fmt.Errorf("VNet address inventory is unavailable")
	}
	var occupied []netip.Prefix
	for _, subnet := range parent.Properties.Subnets {
		if subnet != nil && (strings.EqualFold(resourceValue(subnet.Name), "GatewaySubnet") ||
			strings.EqualFold(resourceValue(subnet.Name), "RouteServerSubnet")) {
			return nil, fmt.Errorf("default topology does not support gateway or route-server address discovery")
		}
		prefixes, err := subnetPrefixes(subnet)
		if err != nil {
			return nil, fmt.Errorf("existing subnet prefix is unknown: %w", err)
		}
		occupied = append(occupied, prefixes...)
	}
	for _, peering := range parent.Properties.VirtualNetworkPeerings {
		if peering == nil || peering.Properties == nil ||
			resourceValue(peering.Properties.PeeringState) != "Connected" ||
			peering.Properties.RemoteAddressSpace == nil ||
			len(peering.Properties.RemoteAddressSpace.AddressPrefixes) == 0 {
			return nil, fmt.Errorf("connected peering address space is unavailable")
		}
		current := peering.Properties.RemoteVirtualNetworkAddressSpace
		if resourceValue(peering.Properties.PeeringSyncLevel) != "" &&
			resourceValue(peering.Properties.PeeringSyncLevel) != "FullyInSync" &&
			(current == nil || len(current.AddressPrefixes) == 0) {
			return nil, fmt.Errorf("current remote peering address space is unavailable")
		}
		for _, space := range []*armnetwork.AddressSpace{peering.Properties.RemoteAddressSpace, current} {
			if space == nil {
				continue
			}
			for _, raw := range space.AddressPrefixes {
				prefix, err := netip.ParsePrefix(resourceValue(raw))
				if err != nil {
					return nil, fmt.Errorf("connected peering prefix is invalid: %w", err)
				}
				occupied = append(occupied, prefix)
			}
		}
		if resourceBool(peering.Properties.AllowGatewayTransit) || resourceBool(peering.Properties.UseRemoteGateways) {
			return nil, fmt.Errorf("default topology does not support gateway-transit address spaces")
		}
	}
	return occupied, nil
}

func validateDefaultPrefix(parent armnetwork.VirtualNetwork, n SubnetPreparation, allowTracked bool) error {
	if parent.Properties == nil || parent.Properties.AddressSpace == nil {
		return fmt.Errorf("R06: parent address space is unavailable")
	}
	prefix, err := netip.ParsePrefix(n.AddressPrefix)
	if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() ||
		prefix != prefix.Masked() || prefix.Bits() != 27 {
		return fmt.Errorf("R06: default delegated subnet must be a canonical private IPv4 /27")
	}
	contained := false
	for _, raw := range parent.Properties.AddressSpace.AddressPrefixes {
		space, err := netip.ParsePrefix(resourceValue(raw))
		if err != nil || space != space.Masked() {
			return fmt.Errorf("R06: parent VNet prefix is invalid")
		}
		contained = contained || space.Contains(prefix.Addr()) && space.Bits() <= prefix.Bits()
	}
	if !contained {
		return fmt.Errorf("R06: planned subnet is outside the current VNet address space")
	}
	properties := *parent.Properties
	if allowTracked {
		for _, subnet := range properties.Subnets {
			if subnet == nil || !strings.EqualFold(resourceValue(subnet.ID), n.SubnetID) {
				continue
			}
			current, err := subnetPrefixes(subnet)
			if err != nil || len(current) != 1 || current[0] != prefix {
				return fmt.Errorf("R06: tracked subnet address differs from the journaled prefix")
			}
		}
		properties.Subnets = slices.DeleteFunc(slices.Clone(properties.Subnets), func(subnet *armnetwork.Subnet) bool {
			return subnet != nil && strings.EqualFold(resourceValue(subnet.ID), n.SubnetID)
		})
	}
	parent.Properties = &properties
	occupied, err := networkParentPrefixes(&parent)
	if err != nil {
		return fmt.Errorf("R06: current VNet address evidence is incomplete: %w", err)
	}
	if slices.ContainsFunc(occupied, prefix.Overlaps) {
		return fmt.Errorf("R06: planned subnet overlaps a current subnet or connected network")
	}
	return nil
}

func privateIPv4Prefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() || !prefix.Addr().Is4() {
		return false
	}
	for _, private := range []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	} {
		if private.Contains(prefix.Addr()) && prefix.Bits() >= private.Bits() {
			return true
		}
	}
	return false
}

func allocateAgentPrefix(parent *armnetwork.VirtualNetwork, occupied []netip.Prefix) (netip.Prefix, error) {
	if parent == nil || parent.Properties == nil || parent.Properties.AddressSpace == nil {
		return netip.Prefix{}, fmt.Errorf("VNet address space is unavailable")
	}
	for _, prefix := range occupied {
		if !prefix.IsValid() || prefix != prefix.Masked() {
			return netip.Prefix{}, fmt.Errorf("occupied address-space evidence is invalid or not canonical")
		}
	}
	var spaces []netip.Prefix
	for _, raw := range parent.Properties.AddressSpace.AddressPrefixes {
		prefix, err := netip.ParsePrefix(resourceValue(raw))
		if err != nil || prefix != prefix.Masked() {
			return netip.Prefix{}, fmt.Errorf("VNet address prefix is invalid or not canonical")
		}
		if privateIPv4Prefix(prefix) && prefix.Bits() <= 27 {
			spaces = append(spaces, prefix)
		}

	}
	slices.SortFunc(spaces, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
	for _, space := range spaces {
		address := space.Addr()
		for address.IsValid() && space.Contains(address) {
			candidate := netip.PrefixFrom(address, 27)
			var conflict *netip.Prefix
			for _, other := range occupied {
				if candidate.Overlaps(other) {
					next := other
					if next.Bits() > candidate.Bits() {
						next = candidate
					}
					conflict = &next
					break
				}
			}
			if conflict == nil {
				return candidate, nil
			}
			value := conflict.Masked().Addr().As4()
			next := uint64(binary.BigEndian.Uint32(value[:])) + uint64(1)<<(32-conflict.Bits())
			if next > uint64(^uint32(0)) {
				break
			}
			var bytes [4]byte
			binary.BigEndian.PutUint32(bytes[:], uint32(next))
			address = netip.AddrFrom4(bytes)
		}
	}
	return netip.Prefix{}, fmt.Errorf("no unused private IPv4 /27 fits the known VNet and connected address spaces")
}
