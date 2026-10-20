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
// N01 uses destination "Storage" (the entire service tag), never the customer's S03 Blob endpoint.
type NetworkRequest struct {
	CheckID         string
	Scope           string
	SourceSubnetIDs []string
	Destination     string
}

// NetworkEvidence is supplied by a supported existing execution context, not a laptop or a newly created probe.
// Static NSG diagnostics and successful node probes alone are not existing-workload DNS/TCP evidence.
type NetworkEvidence struct {
	SourceSubnetIDs             []string
	Destination                 string
	Description                 string
	ExistingWorkload            bool
	DNSVerified                 bool
	TCP443Verified              bool
	TLSVerified                 bool
	ConnectivityFailed          bool
	EffectiveRoutesVerified     bool
	EffectivePolicyVerified     bool
	StorageServiceTagCoverage   bool
	ConnectedAddressSpacesKnown bool
	PrivateEndpointDNSVerified  bool
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
	case "N01":
		if !evidence.StorageServiceTagCoverage {
			base.Missing = "complete outbound TCP 443 Storage service-tag coverage, not one customer Blob endpoint"
			return base
		}
	case "S03":
		if !evidence.ExistingWorkload || !evidence.DNSVerified || !evidence.TCP443Verified ||
			private && !evidence.PrivateEndpointDNSVerified {
			base.Missing = "existing AKS workload DNS/TCP 443 evidence and applicable private endpoint DNS"
			return base
		}
	case "N02":
		if !evidence.ExistingWorkload || !evidence.ConnectedAddressSpacesKnown || !evidence.DNSVerified ||
			!evidence.TCP443Verified {
			base.Missing = "all connected address spaces, effective routes and existing-workload DNS/TCP evidence"
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
			return resourceResult(c, Fail, "delegated subnet overlaps related node/Pod/Service/connected space")
		}
	}
	delegated := slices.ContainsFunc(subnet.Properties.Delegations, func(d *armnetwork.Delegation) bool {
		return d != nil && d.Properties != nil &&
			strings.EqualFold(resourceValue(d.Properties.ServiceName), "Microsoft.App/environments")
	})
	if !delegated {
		return resourceResult(c, Fail, "subnet is not delegated to Microsoft.App/environments")
	}
	c.Observed = "CIDR containment/non-overlap and Microsoft.App/environments delegation verified"
	c.Missing = "complete connected topology, route/NSG/upstream controls and actual DNS/TCP path"
	return c
}

func (c *sdkResourceChecker) networkChecks(
	ctx context.Context, plan Plan, cluster *armcontainerservice.ManagedCluster,
	pools []*armcontainerservice.AgentPool, storage *armstorage.Account,
) (Report, []string) {
	var sources []string
	var related []netip.Prefix
	report := Report{}
	var selected *armcontainerservice.AgentPool
	if cluster != nil {
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
			subnet, checks := c.readNetworkSubnet(ctx, subnetID, "N02")
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
	if cluster != nil && cluster.Properties != nil && cluster.Properties.NetworkProfile != nil {
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
	telemetry := resourceCheck("N01", plan.Cluster.ID, "AKS source subnets allow outbound TCP 443 to Storage service tag")
	telemetry.Observed = "ARM subnet/NSG/route reads are configuration evidence, not complete Storage-tag path proof"
	report = append(report, networkEvidenceCheck(ctx, c.evidence.Network, NetworkRequest{
		CheckID: "N01", Scope: plan.Cluster.ID, SourceSubnetIDs: sources, Destination: "Storage",
	}, telemetry, false))
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
	if !plan.AgentSubnet.Supplied {
		status := NotApplicable
		observed := "no delegated subnet selected"
		if plan.Network != nil {
			observed = "new delegated subnet CIDR planned read-only; actual path is verified after R06 creation"
		} else if plan.AgentSubnet.ID != "" || plan.AgentSubnet.Parent != "" {
			status, observed = Unverified, "planned subnet topology must be resolved; new paths are verified at R06"
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
	if parent != nil && parent.Properties != nil {
		for _, sibling := range parent.Properties.Subnets {
			if sibling == nil || strings.EqualFold(resourceValue(sibling.ID), plan.AgentSubnet.ID) {
				continue
			}
			prefixes, err := subnetPrefixes(sibling)
			if err != nil {
				sourceMetadataKnown = false
			} else {
				related = append(related, prefixes...)
			}
		}
		for _, peering := range parent.Properties.VirtualNetworkPeerings {
			if peering == nil || peering.Properties == nil {
				sourceMetadataKnown = false
				continue
			}
			p := peering.Properties
			if resourceValue(p.PeeringState) != "Connected" {
				report = append(report, resourceResult(resourceCheck("N02", resourceValue(peering.ID),
					"existing peering Connected"), Fail, "peering is not Connected"))
			}
			if p.RemoteAddressSpace == nil {
				sourceMetadataKnown = false
				continue
			}
			if resourceValue(p.PeeringSyncLevel) != "" && resourceValue(p.PeeringSyncLevel) != "FullyInSync" &&
				(p.RemoteVirtualNetworkAddressSpace == nil || len(p.RemoteVirtualNetworkAddressSpace.AddressPrefixes) == 0) {
				sourceMetadataKnown = false
			}
			for _, space := range []*armnetwork.AddressSpace{p.RemoteAddressSpace, p.RemoteVirtualNetworkAddressSpace} {
				if space == nil {
					continue
				}
				for _, raw := range space.AddressPrefixes {
					prefix, err := netip.ParsePrefix(resourceValue(raw))
					if err != nil || prefix != prefix.Masked() {
						sourceMetadataKnown = false
					} else {
						related = append(related, prefix)
					}
				}
			}
		}
	}
	topology := delegatedSubnetCheck(subnet, parent, related)
	if topology.Status != Fail && sourceMetadataKnown {
		topology = networkEvidenceCheck(ctx, c.evidence.Network, NetworkRequest{
			CheckID: "N02", Scope: plan.AgentSubnet.ID, SourceSubnetIDs: sources, Destination: plan.AgentSubnet.ID,
		}, topology, false)
	}
	if !sourceMetadataKnown {
		sources = nil
	}
	return append(report, topology), sources
}

// VerifyPreparedNetwork rechecks actual Blob, companion and registry HTTPS paths.
// No RBAC readback or operator connectivity is substituted for path evidence.
func (c *sdkResourceChecker) VerifyPreparedNetwork(
	ctx context.Context, plan Plan, prepared Prepared,
) (Report, error) {
	plan.Storage = Resource{ID: prepared.StorageID, Supplied: true}
	plan.AgentSubnet = Resource{ID: prepared.AgentSubnetID, Supplied: true}
	cluster, pools, _, report := c.readCluster(ctx, plan.Cluster)
	storage, checks := c.readStorage(ctx, plan.Storage)
	report = append(report, checks...)
	paths, sources := c.networkChecks(ctx, plan, cluster, pools, storage)
	report = append(report, paths...)
	report = append(report, c.registryNetworkChecks(ctx, prepared.RegistryID, sources)...)
	return report, report.Err()
}
