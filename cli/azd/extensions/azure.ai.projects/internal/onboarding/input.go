// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/uuid"
)

// Input contains the existing resources to validate before provisioning.
type Input struct {
	ClusterID, SubscriptionID string
	Network                   Network
}

// Network contains the standard Foundry network selections.
type Network struct {
	VNetID, AgentSubnetName, AgentSubnetPrefix string
	PESubnetName, PESubnetPrefix               string
	CreateAgentSubnet, CreatePESubnet          bool
	DNSResourceGroup, DNSSubscription          string
}

// Validate rejects locally detectable network conflicts before credential or resource reads.
func (n Network) Validate() error {
	if _, err := ParseID(n.VNetID, "Microsoft.Network/virtualNetworks"); err != nil {
		return err
	}
	for _, name := range []string{n.AgentSubnetName, n.PESubnetName} {
		if name == "" || strings.ContainsAny(name, "/\\?#%${} \t\r\n") {
			return fmt.Errorf("network.agentSubnet and network.peSubnet require explicit valid names")
		}
	}
	if strings.EqualFold(n.AgentSubnetName, n.PESubnetName) {
		return fmt.Errorf("network.agentSubnet.name and network.peSubnet.name must differ")
	}
	var prefixes []netip.Prefix
	for _, subnet := range []struct {
		name, prefix  string
		create, agent bool
	}{
		{"agentSubnet", n.AgentSubnetPrefix, n.CreateAgentSubnet, true},
		{"peSubnet", n.PESubnetPrefix, n.CreatePESubnet, false},
	} {
		if subnet.create != (subnet.prefix != "") {
			return fmt.Errorf("network.%s creation requires an explicit prefix; reference mode cannot apply a prefix",
				subnet.name)
		}
		if !subnet.create {
			continue
		}
		prefix, err := netip.ParsePrefix(subnet.prefix)
		if err != nil || prefix != prefix.Masked() || !prefix.Addr().Is4() {
			return fmt.Errorf("network.%s.prefix requires a canonical IPv4 CIDR", subnet.name)
		}
		if subnet.agent && (prefix.Bits() > 27 || !prefix.Addr().IsPrivate() || !prefixLastAddress(prefix).IsPrivate()) {
			return fmt.Errorf("network.agentSubnet.prefix requires a private IPv4 /27 or larger")
		}
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) == 2 && prefixes[0].Overlaps(prefixes[1]) {
		return fmt.Errorf("network.agentSubnet and network.peSubnet prefixes must not overlap")
	}
	if n.DNSResourceGroup == "" && n.DNSSubscription != "" {
		return fmt.Errorf("network.dns.subscription requires network.dns.resourceGroup")
	}
	if len(n.DNSResourceGroup) > 90 || strings.ContainsAny(n.DNSResourceGroup, "/\\?#%${} \t\r\n") ||
		strings.HasSuffix(n.DNSResourceGroup, ".") {
		return fmt.Errorf("network.dns.resourceGroup is invalid")
	}
	if n.DNSSubscription != "" {
		if _, err := uuid.Parse(n.DNSSubscription); err != nil {
			return fmt.Errorf("network.dns.subscription must be a subscription GUID")
		}
	}
	return nil
}

// ParseID requires an exact ARM resource type and complete subscription/RG hierarchy.
func ParseID(value, kind string) (*arm.ResourceID, error) {
	if !strings.HasPrefix(strings.ToLower(value), "/subscriptions/") ||
		strings.ContainsAny(value, "?#%\\@:\x00 \t\r\n") || strings.Contains(value, "//") {
		return nil, fmt.Errorf("expected a complete %s resource ID without URL components", kind)
	}
	id, err := arm.ParseResourceID(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s resource ID: %w", kind, err)
	}
	if id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
		!strings.EqualFold(id.ResourceType.String(), kind) {
		return nil, fmt.Errorf("expected a complete %s resource ID", kind)
	}
	return id, nil
}
