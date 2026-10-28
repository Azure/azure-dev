// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
)

func subnetPrefixes(subnet *armnetwork.Subnet) ([]netip.Prefix, error) {
	if subnet == nil || subnet.Properties == nil {
		return nil, fmt.Errorf("subnet properties are unavailable")
	}
	values := slices.Clone(subnet.Properties.AddressPrefixes)
	if subnet.Properties.AddressPrefix != nil {
		values = append(values, subnet.Properties.AddressPrefix)
	}
	var prefixes []netip.Prefix
	for _, value := range values {
		prefix, err := netip.ParsePrefix(resourceValue(value))
		if err != nil || prefix != prefix.Masked() {
			return nil, fmt.Errorf("subnet requires canonical CIDR prefixes")
		}
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("subnet CIDR evidence is absent")
	}
	return prefixes, nil
}

func delegatedSubnetCheck(subnet *armnetwork.Subnet, related []netip.Prefix) Check {
	check := resourceCheck("N02", resourceValue(subnet.ID),
		"private IPv4 agent subnet, /27 or larger, delegated to Microsoft.App/environments, not overlapping AKS nodes")
	prefixes, err := subnetPrefixes(subnet)
	if err != nil {
		check.Cause = err
		return check
	}
	for _, prefix := range prefixes {
		valid := prefix.Addr().Is4() && prefix.Bits() <= 27 && prefix.Addr().IsPrivate() &&
			prefixLastAddress(prefix).IsPrivate()
		if !valid || slices.ContainsFunc(related, prefix.Overlaps) {
			return resourceResult(check, Fail, "agent subnet is not a private /27-or-larger CIDR disjoint from AKS nodes")
		}
	}
	delegated := slices.ContainsFunc(subnet.Properties.Delegations, func(delegation *armnetwork.Delegation) bool {
		return delegation != nil && delegation.Properties != nil &&
			strings.EqualFold(resourceValue(delegation.Properties.ServiceName), "Microsoft.App/environments")
	})
	return resourceKnown(check, true, delegated, "agent subnet CIDR and Microsoft.App/environments delegation")
}

func prefixLastAddress(prefix netip.Prefix) netip.Addr {
	last := prefix.Addr().As16()
	bits := prefix.Bits()
	if prefix.Addr().Is4() {
		bits += 96
	}
	for bit := bits; bit < 128; bit++ {
		last[bit/8] |= 1 << (7 - bit%8)
	}
	return netip.AddrFrom16(last).Unmap()
}

func (c *sdkResourceChecker) readSubnet(ctx context.Context, raw string) (*armnetwork.Subnet, error) {
	id, err := ParseID(raw, "Microsoft.Network/virtualNetworks/subnets")
	if err != nil {
		return nil, err
	}
	client, err := armnetwork.NewSubnetsClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, err
	}
	result, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
	if err != nil {
		return nil, err
	}
	return &result.Subnet, nil
}

func (c *sdkResourceChecker) selectedSubnet(
	ctx context.Context, network Network, name, prefix string, create, delegated bool,
) (*armnetwork.Subnet, error) {
	id := network.VNetID + "/subnets/" + name
	if !create {
		return c.readSubnet(ctx, id)
	}
	if prefix == "" {
		return nil, fmt.Errorf("creating a subnet requires an explicit prefix")
	}
	subnet := &armnetwork.Subnet{ID: new(id), Name: new(name),
		Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: new(prefix)}}
	if delegated {
		subnet.Properties.Delegations = []*armnetwork.Delegation{{
			Properties: &armnetwork.ServiceDelegationPropertiesFormat{ServiceName: new("Microsoft.App/environments")},
		}}
	}
	return subnet, nil
}

func (c *sdkResourceChecker) networkChecks(
	ctx context.Context, plan Plan, cluster *armcontainerservice.ManagedCluster, pool *armcontainerservice.AgentPool,
) Report {
	network := plan.Network
	vnetID, err := ParseID(network.VNetID, "Microsoft.Network/virtualNetworks")
	if err != nil {
		return Report{readFailure("N02", network.VNetID, "complete VNet ID", err)}
	}
	client, err := armnetwork.NewVirtualNetworksClient(vnetID.SubscriptionID, c.credential, c.options)
	if err != nil {
		return Report{readFailure("N02", network.VNetID, "virtualNetworks/read", err)}
	}
	vnet, err := client.Get(ctx, vnetID.ResourceGroupName, vnetID.Name, nil)
	if err != nil {
		return Report{readFailure("N02", network.VNetID, "virtualNetworks/read", err)}
	}
	if vnet.Properties == nil || vnet.Properties.AddressSpace == nil {
		return Report{resourceCheck("N02", network.VNetID, "readable VNet address space")}
	}
	report := Report{resourceReadiness(network.VNetID, resourceValue(vnet.ID),
		resourceValue(vnet.Properties.ProvisioningState), "ready selected VNet")}
	var parents []netip.Prefix
	for _, raw := range vnet.Properties.AddressSpace.AddressPrefixes {
		prefix, err := netip.ParsePrefix(resourceValue(raw))
		if err != nil || prefix != prefix.Masked() {
			return append(report, resourceCheck("N02", network.VNetID, "canonical VNet address prefixes"))
		}
		parents = append(parents, prefix)
	}
	nodeIDs, checks := c.nodeSubnetIDs(ctx, plan, cluster, pool)
	report = append(report, checks...)
	if len(nodeIDs) == 0 {
		return report
	}
	var nodes []netip.Prefix
	for _, id := range nodeIDs {
		subnet, err := c.readSubnet(ctx, id)
		if err != nil {
			return append(report, readFailure("N02", id, "selected AKS pool node subnet read", err))
		}
		if subnet.Properties == nil {
			return append(report, resourceCheck("N02", id, "readable AKS node subnet properties"))
		}
		report = append(report, resourceReadiness(id, resourceValue(subnet.ID),
			resourceValue(subnet.Properties.ProvisioningState), "ready AKS node subnet"))
		prefixes, err := subnetPrefixes(subnet)
		if err != nil {
			return append(report, readFailure("N02", id, "parseable AKS node subnet CIDRs", err))
		}
		nodes = append(nodes, prefixes...)
	}
	var selected []*armnetwork.Subnet
	var prefixes [][]netip.Prefix
	for _, item := range []struct {
		name, prefix      string
		create, delegated bool
	}{
		{network.AgentSubnetName, network.AgentSubnetPrefix, network.CreateAgentSubnet, true},
		{network.PESubnetName, network.PESubnetPrefix, network.CreatePESubnet, false},
	} {
		id := network.VNetID + "/subnets/" + item.name
		subnet, err := c.selectedSubnet(ctx, network, item.name, item.prefix, item.create, item.delegated)
		if err != nil {
			return append(report, readFailure("N02", id, "selected standard network subnet", err))
		}
		if subnet.Properties == nil {
			return append(report, resourceCheck("N02", id, "readable subnet properties"))
		}
		if !item.create {
			report = append(report, resourceReadiness(id, resourceValue(subnet.ID),
				resourceValue(subnet.Properties.ProvisioningState), "ready selected subnet"))
		}
		cidrs, err := subnetPrefixes(subnet)
		if err != nil {
			return append(report, readFailure("N02", id, "canonical subnet CIDRs", err))
		}
		for _, prefix := range cidrs {
			contained := slices.ContainsFunc(parents, func(parent netip.Prefix) bool {
				return parent.Bits() <= prefix.Bits() && parent.Contains(prefix.Addr()) &&
					parent.Contains(prefixLastAddress(prefix))
			})
			if !contained {
				return append(report, resourceResult(resourceCheck("N02", id, "subnet within selected VNet"),
					Fail, "subnet CIDR is not contained in the selected VNet"))
			}
			if item.create {
				for _, existing := range vnet.Properties.Subnets {
					if existing == nil {
						return append(report, resourceCheck("N02", id, "complete existing subnet CIDR inventory"))
					}
					if strings.EqualFold(resourceValue(existing.ID), id) {
						continue
					}
					existingPrefixes, err := subnetPrefixes(existing)
					if err != nil {
						return append(report, readFailure("N02", id, "readable existing subnet inventory", err))
					}
					if slices.ContainsFunc(existingPrefixes, prefix.Overlaps) {
						return append(report, resourceResult(resourceCheck("N02", id, "available subnet CIDR"),
							Fail, "requested subnet prefix overlaps another existing subnet"))
					}
				}
			}
		}
		if !item.delegated && len(subnet.Properties.Delegations) != 0 {
			return append(report, resourceResult(resourceCheck("N02", id, "non-delegated private endpoint subnet"),
				Fail, "private endpoint subnet is delegated"))
		}
		selected = append(selected, subnet)
		prefixes = append(prefixes, cidrs)
	}
	for _, prefix := range prefixes[0] {
		if slices.ContainsFunc(prefixes[1], prefix.Overlaps) {
			return append(report, resourceResult(resourceCheck("N02", network.VNetID, "disjoint agent and PE subnets"),
				Fail, "agent and private endpoint subnet prefixes overlap"))
		}
	}
	return append(report, delegatedSubnetCheck(selected[0], nodes))
}
