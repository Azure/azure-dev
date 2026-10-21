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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

// NetworkRequest binds evidence to the selected source subnets and the precise requirement.
type NetworkRequest struct {
	CheckID         string
	Scope           string
	SourceSubnetIDs []string
	Destination     string
}

// NetworkEvidence is supplied by a supported existing execution context, not a laptop or a newly created probe.
// Static NSG diagnostics and successful node probes alone are not existing-workload DNS/TCP evidence.
type NetworkEvidence struct {
	SourceSubnetIDs            []string
	Destination                string
	Description                string
	ExistingWorkload           bool
	DNSVerified                bool
	TCP443Verified             bool
	TLSVerified                bool
	ConnectivityFailed         bool
	EffectiveRoutesVerified    bool
	EffectivePolicyVerified    bool
	PrivateEndpointDNSVerified bool
}

// NetworkEvidenceProvider only reads evidence from supported existing workload contexts.
// Implementations must not create pods/extensions, use SSH, mutate AKS, or infer success from operator connectivity.
type NetworkEvidenceProvider interface {
	Evidence(context.Context, NetworkRequest) (NetworkEvidence, error)
}

func networkEvidenceCheck(
	ctx context.Context, provider NetworkEvidenceProvider, request NetworkRequest, base Check, private bool,
) Check {
	if provider == nil {
		base.Missing = "supported existing-workload evidence for " + request.CheckID +
			"; effective source/SNAT, routes, upstream controls, DNS and TCP 443"
		base.Remediation = "supply evidence from a supported existing AKS workload path; no probe resources are created"
		return base
	}
	evidence, err := provider.Evidence(ctx, request)
	if err != nil {
		base.Cause = err
		base.Observed += "; existing-path evidence could not be read"
		return base
	}
	if len(request.SourceSubnetIDs) == 0 || evidence.Description == "" ||
		evidence.Destination != request.Destination || !evidence.EffectiveRoutesVerified ||
		!evidence.EffectivePolicyVerified {
		base.Missing = "source-bound destination, effective route/policy and descriptive evidence"
		return base
	}
	for _, source := range request.SourceSubnetIDs {
		if !slices.ContainsFunc(evidence.SourceSubnetIDs, func(candidate string) bool {
			return strings.EqualFold(source, candidate)
		}) {
			base.Missing = "evidence for actual AKS source subnet " + source
			return base
		}
	}
	if evidence.ConnectivityFailed && evidence.ExistingWorkload {
		return resourceResult(base, Fail, "source-bound existing-workload connectivity evidence reports failure")
	}
	switch request.CheckID {
	case "S03":
		if !evidence.ExistingWorkload || !evidence.DNSVerified || !evidence.TCP443Verified ||
			private && !evidence.PrivateEndpointDNSVerified {
			base.Missing = "existing AKS workload DNS/TCP 443 evidence and applicable private endpoint DNS"
			return base
		}
	case "R06":
		if !evidence.ExistingWorkload || !evidence.DNSVerified || !evidence.TCP443Verified ||
			!evidence.TLSVerified || private && !evidence.PrivateEndpointDNSVerified {
			base.Missing = "existing AKS workload DNS/TCP 443/TLS evidence and applicable private endpoint DNS"
			return base
		}
	}
	// Provider descriptions are not printed: they may contain credential-bearing diagnostic URLs.
	return resourceResult(base, Pass, base.Observed+"; source-bound supported network evidence verified")
}

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
	c := resourceCheck("N02", resourceValue(subnet.ID),
		"private non-overlapping delegated subnet, at least /27, contained in its VNet")
	prefixes, err := subnetPrefixes(subnet)
	if err != nil {
		c.Observed, c.Cause = "subnet CIDR evidence could not be validated", err
		return c
	}
	if parent == nil || parent.Properties == nil || parent.Properties.AddressSpace == nil {
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
			return resourceResult(c, Fail, "delegated subnet overlaps AKS node/Pod/Service, sibling-subnet or direct-peering ranges")
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

func (c *sdkResourceChecker) networkChecks(
	ctx context.Context, plan Plan, cluster *armcontainerservice.ManagedCluster,
	pools []*armcontainerservice.AgentPool, storage *armstorage.Account,
) (Report, []string) {
	return c.networkConfiguration(ctx, plan, cluster, pools, storage, true)
}

func (c *sdkResourceChecker) networkConfiguration(
	ctx context.Context, plan Plan, cluster *armcontainerservice.ManagedCluster,
	pools []*armcontainerservice.AgentPool, storage *armstorage.Account, checkTopology bool,
) (Report, []string) {
	var sources []string
	var related []netip.Prefix
	report := Report{}
	checkSubnet := checkTopology && plan.AgentSubnet.Supplied
	checkSources := plan.Storage.Supplied
	var selected *armcontainerservice.AgentPool
	if cluster != nil && (checkSubnet || checkSources) {
		// The SKU-aware choice is made from the same child resources as A06-A10.
		id, err := existingResourceID(plan.Cluster.ID, "Microsoft.ContainerService/managedClusters")
		if err == nil {
			skus, err := c.readSKUs(ctx, id.SubscriptionID, resourceValue(cluster.Location))
			if err == nil {
				selected, _ = selectedAgentPool(*cluster, pools, skus)
			}
		}
	}
	sourceMetadataKnown := selected != nil
	for _, pool := range pools {
		if pool == nil || pool.Properties == nil || pool != selected {
			continue
		}

		p := pool.Properties
		for _, subnetID := range []string{resourceValue(p.VnetSubnetID), resourceValue(p.PodSubnetID)} {
			if subnetID == "" {
				if pool == selected && resourceValue(p.VnetSubnetID) == "" {
					sourceMetadataKnown = false
				}
				continue
			}
			checkID := "S03"
			if checkSubnet {
				checkID = "N02"
			}
			subnet, checks := c.readNetworkSubnet(ctx, subnetID, checkID)
			report = append(report, checks...)
			if subnet != nil {
				prefixes, err := subnetPrefixes(subnet)
				if err == nil {
					related = append(related, prefixes...)
				} else {
					sourceMetadataKnown = false
				}
			} else {
				sourceMetadataKnown = false
			}
			if pool == selected {
				sources = append(sources, subnetID)
			}
		}
	}
	slices.Sort(sources)
	sources = slices.Compact(sources)
	if !sourceMetadataKnown {
		sources = nil
	}
	if checkSubnet && cluster != nil && cluster.Properties != nil && cluster.Properties.NetworkProfile != nil {
		n := cluster.Properties.NetworkProfile
		cidrs := append(slices.Clone(n.PodCidrs), n.ServiceCidrs...)
		cidrs = append(cidrs, n.PodCidr, n.ServiceCidr)
		for _, raw := range cidrs {
			if raw == nil {
				continue
			}
			prefix, err := netip.ParsePrefix(*raw)
			if err == nil {
				related = append(related, prefix)
			} else {
				sourceMetadataKnown = false
			}
		}
		if !sourceMetadataKnown {
			sources = nil
		}
	}
	blob := resourceCheck("S03", plan.Storage.ID, "existing AKS workload resolves and reaches Blob endpoint on TCP 443")
	if !plan.Storage.Supplied {
		report = append(report, resourceResult(blob, NotApplicable, "new Storage path is validated during R06"))
	} else if storage == nil || storage.Properties == nil || storage.Properties.PrimaryEndpoints == nil {
		report = append(report, blob)
	} else {
		p := storage.Properties
		endpoint := safeResourceEndpoint(resourceValue(p.PrimaryEndpoints.Blob))
		access := resourceValue(p.PublicNetworkAccess)
		action := ""
		if p.NetworkRuleSet != nil {
			action = resourceValue(p.NetworkRuleSet.DefaultAction)
		}
		private := access == "Disabled" || action == "Deny"
		blob.Observed = "Blob endpoint=" + endpoint + "; publicNetworkAccess=" + access + "; defaultAction=" + action
		if endpoint == "" || access != "Enabled" && access != "Disabled" ||
			action != "Allow" && action != "Deny" {
			blob.Missing = "valid HTTPS Blob endpoint and explicit public/firewall access settings"
		} else {
			// Enabled/Allow is valid. It is not DNS/TCP proof, and does not imply anonymous Blob access.
			report = append(report, c.storagePrivateEndpoints(ctx, storage)...)
			blob = networkEvidenceCheck(ctx, c.evidence.Network, NetworkRequest{
				CheckID: "S03", Scope: plan.Storage.ID, SourceSubnetIDs: sources, Destination: endpoint,
			}, blob, private)
		}
		report = append(report, blob)
	}
	if !checkTopology {
		return report, sources
	}
	report = append(report, c.staticStorageNetwork(ctx, storage)...)
	if !plan.AgentSubnet.Supplied {
		status := NotApplicable
		observed := "no delegated subnet selected"
		if plan.Network != nil {
			status = Pass
			observed = "static subnet plan resolved read-only; selected write authority is evaluated by P09"
		} else if plan.AgentSubnet.ID != "" || plan.AgentSubnet.Parent != "" {
			status, observed = Unverified, "planned subnet topology must be resolved before preparation"
		}
		report = append(report, resourceResult(resourceCheck("N02", plan.AgentSubnet.ID,
			"selected supported companion topology"), status, observed))
		return report, sources
	}
	subnet, checks := c.readNetworkSubnet(ctx, plan.AgentSubnet.ID, "N02")
	report = append(report, checks...)
	if subnet == nil {
		return report, sources
	}
	parent, checks := c.readParentNetwork(ctx, plan.AgentSubnet.ID)
	report = append(report, checks...)
	var inventoryErr error
	if parent != nil && parent.Properties != nil && cluster != nil {
		for _, peering := range parent.Properties.VirtualNetworkPeerings {
			if peering != nil && peering.Properties != nil &&
				resourceValue(peering.Properties.PeeringState) != "Connected" {
				report = append(report, resourceResult(resourceCheck("N02", resourceValue(peering.ID),
					"existing peering Connected"), Fail, "peering is not Connected"))
			}
		}
		siblings := *parent
		properties := *parent.Properties
		properties.Subnets = slices.DeleteFunc(slices.Clone(properties.Subnets), func(sibling *armnetwork.Subnet) bool {
			return sibling != nil && strings.EqualFold(resourceValue(sibling.ID), plan.AgentSubnet.ID)
		})
		siblings.Properties = &properties
		var occupied []netip.Prefix
		occupied, inventoryErr = networkOccupiedPrefixes(*cluster, &siblings)
		related = append(related, occupied...)
	} else {
		inventoryErr = fmt.Errorf("AKS and Agent subnet VNet address inventory is unavailable")
	}
	topology := delegatedSubnetCheck(subnet, parent, related)
	if inventoryErr != nil {
		sourceMetadataKnown = false
		if topology.Status != Fail {
			topology.Status = Unverified
		}
		topology.Cause = inventoryErr
		topology.Missing = "AKS node/Pod/Service, sibling-subnet and direct-peering address evidence"
	}
	if !sourceMetadataKnown && topology.Status != Fail {
		topology.Status = Unverified
		topology.Missing = "AKS node/Pod/Service, sibling-subnet and direct-peering ARM metadata"
	}
	if !sourceMetadataKnown {
		sources = nil
	}
	report = append(report, topology)
	return report, sources
}

// VerifyPreparedNetwork verifies independent S03 and registry paths, not N02.
// Created network resources have their own ARM creation-contract readback in prepareNetwork.
func (c *sdkResourceChecker) VerifyPreparedNetwork(
	ctx context.Context, plan Plan, prepared Prepared,
) (Report, error) {
	plan.Storage = Resource{ID: prepared.StorageID, Supplied: true}
	cluster, pools, _, report := c.readCluster(ctx, plan.Cluster)
	storage, checks := c.readStorage(ctx, plan.Storage)
	report = append(report, checks...)
	paths, sources := c.networkConfiguration(ctx, plan, cluster, pools, storage, false)
	report = append(report, paths...)
	report = append(report, c.registryNetworkChecks(ctx, prepared.RegistryID, sources)...)
	return report, report.Err()
}
