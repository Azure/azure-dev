// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore armnetwork parseable

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
		if err != nil {
			return nil, fmt.Errorf("invalid subnet prefix: %w", err)
		}
		if prefix != prefix.Masked() {
			return nil, fmt.Errorf("subnet CIDR must use its network address")
		}
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("subnet CIDR evidence is absent")
	}
	return prefixes, nil
}

func delegatedSubnetCheck(
	subnet *armnetwork.Subnet, parent *armnetwork.VirtualNetwork, related []netip.Prefix,
) Check {
	c := resourceCheck("agent-subnet", resourceValue(subnet.ID),
		"private non-overlapping delegated subnet, at least /27, contained in its VNet")
	prefixes, err := subnetPrefixes(subnet)
	if err != nil {
		c.Observed, c.Cause = "subnet CIDR evidence could not be validated", err
		return c
	}
	if parent == nil || parent.Properties == nil || parent.Properties.AddressSpace == nil ||
		len(parent.Properties.AddressSpace.AddressPrefixes) == 0 {
		c.Missing = "parent VNet address space"
		return c
	}
	var parents []netip.Prefix
	for _, raw := range parent.Properties.AddressSpace.AddressPrefixes {
		prefix, err := netip.ParsePrefix(resourceValue(raw))
		if err != nil {
			c.Cause = err
			c.Missing = "parseable parent address spaces"
			return c
		}
		parents = append(parents, prefix)
	}
	for _, prefix := range prefixes {
		// netip.Addr.IsPrivate includes RFC1918 and IPv6 ULA. Both ends must be private.
		last := prefix.Addr().As16()
		bits := prefix.Bits()
		if prefix.Addr().Is4() {
			bits += 96
		}
		for bit := bits; bit < 128; bit++ {
			last[bit/8] |= 1 << (7 - bit%8)
		}
		end := netip.AddrFrom16(last).Unmap()
		valid := prefix.Addr().Is4() && prefix.Bits() <= 27 && prefix.Addr().IsPrivate() && end.IsPrivate()
		contained := slices.ContainsFunc(parents, func(parent netip.Prefix) bool {
			return parent.Bits() <= prefix.Bits() && parent.Contains(prefix.Addr()) && parent.Contains(end)
		})
		if !valid || !contained {
			return resourceResult(c, Fail, "subnet must be a private IPv4 CIDR, /27 or larger, within parent VNet")
		}
		if slices.ContainsFunc(related, prefix.Overlaps) {
			return resourceResult(c, Fail, "delegated subnet overlaps the selected AKS pool Node subnet")
		}
	}
	delegated := slices.ContainsFunc(subnet.Properties.Delegations, func(d *armnetwork.Delegation) bool {
		return d != nil && d.Properties != nil &&
			strings.EqualFold(resourceValue(d.Properties.ServiceName), "Microsoft.App/environments")
	})
	if !delegated {
		return resourceResult(c, Fail, "subnet is not delegated to Microsoft.App/environments")
	}
	return resourceResult(c, Pass, "ARM CIDR containment/non-overlap and Microsoft.App/environments delegation verified")
}

func (c *sdkResourceChecker) selectAgentPool(
	ctx context.Context, plan Plan, cluster *armcontainerservice.ManagedCluster,
	pools []*armcontainerservice.AgentPool,
) (*armcontainerservice.AgentPool, Report) {
	networks := map[*armcontainerservice.AgentPool]Report{}
	for _, pool := range pools {
		if ctx.Err() != nil {
			break
		}
		if pool == nil || pool.Properties == nil || resourceValue(pool.Properties.Mode) != "User" ||
			slices.ContainsFunc(agentPoolChecks(pool), Check.blocks) {
			continue
		}
		network := c.networkChecks(ctx, plan, cluster, pool)
		networks[pool] = network
		if !slices.ContainsFunc(network, Check.blocks) {
			return pool, network
		}
	}
	// With no fully qualifying pool, retain the original configuration ranking for diagnostics only.
	selected, _ := selectedAgentPool(pools)
	network, checked := networks[selected]
	if !checked {
		network = c.networkChecks(ctx, plan, cluster, selected)
	}
	return selected, network
}

func (c *sdkResourceChecker) networkChecks(
	ctx context.Context, plan Plan, cluster *armcontainerservice.ManagedCluster,
	selected *armcontainerservice.AgentPool,
) Report {
	report := Report{}
	checkSubnet := plan.AgentSubnet.ID != "" && plan.AgentSubnet.Supplied &&
		plan.AgentSubnet.Missing == "" && plan.AgentSubnet.LifecycleMissing == ""
	var related []netip.Prefix
	var nodeMetadataKnown bool
	var nodeReport, parentReport, subnetReport Report
	var parent *armnetwork.VirtualNetwork
	var subnet *armnetwork.Subnet
	if checkSubnet {
		parallelReads(func() {
			related, nodeMetadataKnown, nodeReport = c.readNodeSubnetPrefixes(ctx, plan, cluster, selected)
		}, func() {
			parent, parentReport = c.readParentNetwork(ctx, plan.AgentSubnet.ID)
		}, func() {
			subnet, subnetReport = c.readNetworkSubnet(ctx, plan.AgentSubnet.ID, "agent-subnet")
		})
	}
	report = append(report, nodeReport...)
	if missing := plan.AgentSubnet.Missing + plan.AgentSubnet.LifecycleMissing; missing != "" {
		return append(report, SkippedCheck("agent-subnet", plan.AgentSubnet.diagnosticScope(),
			"existing Agent subnet configuration", missing))
	}
	if !checkSubnet {
		report = append(report, resourceResult(resourceCheck("agent-subnet", plan.AgentSubnet.ID,
			"existing standard network.agentSubnet selection"), NotApplicable,
			"no existing customer Agent subnet selected; standard network configuration is unchanged"))
		return report
	}
	report = append(report, parentReport...)
	report = append(report, subnetReport...)
	if subnet == nil {
		return report
	}
	subnetCheck := delegatedSubnetCheck(subnet, parent, related)
	if plan.Cluster.Missing != "" {
		if subnetCheck.Status == Pass {
			subnetCheck.Expected = "private IPv4 CIDR, at least /27, contained in its VNet and delegated"
			subnetCheck.Observed = "ARM CIDR containment and Microsoft.App/environments delegation verified"
		}
		return append(report, subnetCheck, SkippedCheck("agent-subnet", plan.AgentSubnet.ID,
			"selected AKS Node subnet non-overlap", plan.Cluster.Missing))
	}
	if !nodeMetadataKnown && subnetCheck.Status == Pass {
		subnetCheck.Status = Unverified
		subnetCheck.Observed = "ARM CIDR containment and Microsoft.App/environments delegation verified; " +
			"non-overlap with the selected AKS pool Node subnet could not be verified"
		subnetCheck.Missing = "selected AKS pool Node subnet ARM CIDR metadata"
		subnetCheck.Remediation = "ensure the selected AKS pool Node subnet CIDRs are readable and complete, " +
			"then rerun preflight to verify non-overlap"
	}
	report = append(report, subnetCheck)
	return report
}

func (c *sdkResourceChecker) readNodeSubnetPrefixes(
	ctx context.Context, plan Plan, cluster *armcontainerservice.ManagedCluster, selected *armcontainerservice.AgentPool,
) ([]netip.Prefix, bool, Report) {
	var related []netip.Prefix
	var report Report
	nodeMetadataKnown := false
	if selected != nil && selected.Properties != nil {
		subnetIDs, checks := c.nodeSubnetIDs(ctx, plan, cluster, selected)
		report = append(report, checks...)
		nodeMetadataKnown = len(subnetIDs) > 0
		for _, subnetID := range subnetIDs {
			subnet, checks := c.readNetworkSubnet(ctx, subnetID, "agent-subnet")
			report = append(report, checks...)
			if subnet != nil {
				state := ""
				if subnet.Properties != nil {
					state = resourceValue(subnet.Properties.ProvisioningState)
				}
				readiness := resourceReadiness(subnetID, resourceValue(subnet.ID), state, "subnets/read")
				report = append(report, readiness)
				prefixes, err := subnetPrefixes(subnet)
				if err != nil {
					metadata := resourceCheck("agent-subnet", subnetID, "parseable selected AKS pool Node subnet CIDRs")
					metadata.Missing = "ARM subnet addressPrefix/addressPrefixes"
					metadata.Cause = err
					report = append(report, metadata)
				}
				if err == nil && readiness.Status == Pass {
					related = append(related, prefixes...)
				} else {
					nodeMetadataKnown = false
				}
			} else {
				nodeMetadataKnown = false
			}
		}
	}
	return related, nodeMetadataKnown, report
}
