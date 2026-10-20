// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"net/netip"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

func staticNetworkReadiness(scope, returnedID, state string) Check {
	check := resourceReadiness(scope, returnedID, state, "ARM network configuration")
	check.ID = "N02"
	return check
}

func staticSecurityGroup(group armnetwork.SecurityGroup, scope string) Check {
	check := staticNetworkReadiness(scope, resourceValue(group.ID), "")
	if group.Properties == nil {
		return check
	}
	check = staticNetworkReadiness(scope, resourceValue(group.ID), resourceValue(group.Properties.ProvisioningState))
	if check.Status != Pass {
		return check
	}
	rules := append(slices.Clone(group.Properties.SecurityRules), group.Properties.DefaultSecurityRules...)
	if len(rules) == 0 {
		return resourceResult(check, Unverified, "associated NSG rule metadata is unavailable")
	}
	for _, rule := range rules {
		if rule == nil || rule.Properties == nil {
			return resourceResult(check, Unverified, "associated NSG rule properties are unavailable")
		}
		p := rule.Properties
		if p.Priority == nil || p.Direction == nil || p.Access == nil || p.Protocol == nil ||
			(p.SourceAddressPrefix == nil && len(p.SourceAddressPrefixes) == 0 &&
				len(p.SourceApplicationSecurityGroups) == 0) ||
			(p.DestinationAddressPrefix == nil && len(p.DestinationAddressPrefixes) == 0 &&
				len(p.DestinationApplicationSecurityGroups) == 0) ||
			(p.SourcePortRange == nil && len(p.SourcePortRanges) == 0) ||
			(p.DestinationPortRange == nil && len(p.DestinationPortRanges) == 0) {
			return resourceResult(check, Unverified, "associated NSG rule selectors are incomplete")
		}
		if *p.Priority <= 0 || *p.Priority > 65535 ||
			(*p.Direction != armnetwork.SecurityRuleDirectionInbound && *p.Direction != armnetwork.SecurityRuleDirectionOutbound) ||
			(*p.Access != armnetwork.SecurityRuleAccessAllow && *p.Access != armnetwork.SecurityRuleAccessDeny) ||
			!slices.Contains(armnetwork.PossibleSecurityRuleProtocolValues(), *p.Protocol) {
			return resourceResult(check, Fail, "associated NSG rule has invalid ARM priority/direction/access/protocol")
		}
	}
	return resourceResult(check, Pass, "associated NSG and declared ARM rules validated; no live-path claim")
}

func staticRouteTable(table armnetwork.RouteTable, scope string) Check {
	check := staticNetworkReadiness(scope, resourceValue(table.ID), "")
	if table.Properties == nil {
		return check
	}
	check = staticNetworkReadiness(scope, resourceValue(table.ID), resourceValue(table.Properties.ProvisioningState))
	if check.Status != Pass {
		return check
	}
	for _, route := range table.Properties.Routes {
		if route == nil || route.Properties == nil || route.Properties.AddressPrefix == nil ||
			route.Properties.NextHopType == nil {
			return resourceResult(check, Unverified, "associated route metadata is incomplete")
		}
		p := route.Properties
		// ARM routes also accept service-tag names; only CIDR-shaped selectors are parsed as CIDRs.
		if strings.Contains(*p.AddressPrefix, "/") {
			prefix, err := netip.ParsePrefix(*p.AddressPrefix)
			if err != nil || prefix != prefix.Masked() {
				return resourceResult(check, Fail, "associated route has an invalid CIDR destination")
			}
		} else if strings.TrimSpace(*p.AddressPrefix) == "" {
			return resourceResult(check, Fail, "associated route destination is empty")
		}
		if *p.NextHopType == armnetwork.RouteNextHopTypeVirtualAppliance {
			if _, err := netip.ParseAddr(resourceValue(p.NextHopIPAddress)); err != nil {
				return resourceResult(check, Fail, "VirtualAppliance route requires a valid next-hop IP")
			}
		} else if !slices.Contains(armnetwork.PossibleRouteNextHopTypeValues(), *p.NextHopType) {
			return resourceResult(check, Fail, "associated route has an unsupported ARM next-hop type")
		}
	}
	return resourceResult(check, Pass, "associated ARM routes validated; external next-hop internal policy is not inspected")
}

func (c *sdkResourceChecker) staticStorageNetwork(ctx context.Context, storage *armstorage.Account) Report {
	if storage == nil || storage.Properties == nil {
		return nil // The resource-read check reports missing supplied Storage; omitted Storage is a planned resource.
	}
	p := storage.Properties
	check := resourceCheck("N02", resourceValue(storage.ID), "readable applicable Storage ARM network configuration")
	if p.PublicNetworkAccess == nil || p.NetworkRuleSet == nil || p.NetworkRuleSet.DefaultAction == nil {
		return Report{check}
	}
	check = resourceKnown(check, true,
		(*p.PublicNetworkAccess == armstorage.PublicNetworkAccessEnabled ||
			*p.PublicNetworkAccess == armstorage.PublicNetworkAccessDisabled) &&
			(*p.NetworkRuleSet.DefaultAction == armstorage.DefaultActionAllow ||
				*p.NetworkRuleSet.DefaultAction == armstorage.DefaultActionDeny),
		"Storage public-network and firewall configuration validated independently of S03 path evidence")
	report := Report{check}
	for _, rule := range p.NetworkRuleSet.VirtualNetworkRules {
		ruleCheck := resourceCheck("N02", resourceValue(storage.ID), "valid Storage virtual-network rule ARM configuration")
		if rule == nil || rule.VirtualNetworkResourceID == nil {
			report = append(report, ruleCheck)
			continue
		}
		_, err := existingResourceID(*rule.VirtualNetworkResourceID, "Microsoft.Network/virtualNetworks/subnets")
		report = append(report, resourceKnown(ruleCheck, true, err == nil,
			"Storage virtual-network rule ARM configuration"))
	}
	for _, rule := range p.NetworkRuleSet.IPRules {
		ruleCheck := resourceCheck("N02", resourceValue(storage.ID), "valid Storage IP firewall rule ARM configuration")
		if rule == nil || rule.IPAddressOrRange == nil {
			report = append(report, ruleCheck)
			continue
		}
		address, addressErr := netip.ParseAddr(*rule.IPAddressOrRange)
		prefix, prefixErr := netip.ParsePrefix(*rule.IPAddressOrRange)
		report = append(report, resourceKnown(ruleCheck, true,
			addressErr == nil && address.Is4() || prefixErr == nil && prefix.Addr().Is4() && prefix == prefix.Masked(),
			"Storage IP firewall rule ARM configuration"))
	}
	for _, connection := range p.PrivateEndpointConnections {
		approval := resourceCheck("N02", resourceValue(storage.ID), "readable Private Endpoint approval")
		if connection == nil || connection.Properties == nil ||
			connection.Properties.PrivateLinkServiceConnectionState == nil ||
			connection.Properties.PrivateEndpoint == nil || connection.Properties.PrivateEndpoint.ID == nil {
			report = append(report, approval)
			continue
		}
		state := resourceValue(connection.Properties.PrivateLinkServiceConnectionState.Status)
		report = append(report, resourceKnown(approval, state != "", state == "Approved", "approval="+state))
		report = append(report, c.staticPrivateEndpoint(ctx, *connection.Properties.PrivateEndpoint.ID)...)
	}
	return report
}

func (c *sdkResourceChecker) staticPrivateEndpoint(ctx context.Context, raw string) Report {
	id, err := existingResourceID(raw, "Microsoft.Network/privateEndpoints")
	if err != nil {
		return Report{readFailure("N02", raw, "Microsoft.Network/privateEndpoints/read", err)}
	}
	client, err := armnetwork.NewPrivateEndpointsClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return Report{readFailure("N02", raw, "Microsoft.Network/privateEndpoints/read", err)}
	}
	endpoint, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return Report{readFailure("N02", raw, "Microsoft.Network/privateEndpoints/read", err)}
	}
	state := ""
	if endpoint.Properties != nil {
		state = resourceValue(endpoint.Properties.ProvisioningState)
	}
	report := Report{staticNetworkReadiness(raw, resourceValue(endpoint.ID), state)}
	groups, err := armnetwork.NewPrivateDNSZoneGroupsClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return append(report, readFailure("N02", raw, "Microsoft.Network/privateEndpoints/privateDnsZoneGroups/read", err))
	}
	pager := groups.NewListPager(id.ResourceGroupName, id.Name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return append(report, readFailure("N02", raw, "Microsoft.Network/privateEndpoints/privateDnsZoneGroups/read", err))
		}
		for _, group := range page.Value {
			check := resourceCheck("N02", raw, "readable Private DNS zone group ARM configuration")
			if group == nil || group.Properties == nil || len(group.Properties.PrivateDNSZoneConfigs) == 0 {
				report = append(report, check)
				continue
			}
			for _, config := range group.Properties.PrivateDNSZoneConfigs {
				if config == nil || config.Properties == nil || config.Properties.PrivateDNSZoneID == nil {
					report = append(report, check)
					continue
				}
				report = append(report, c.staticPrivateDNS(ctx, *config.Properties.PrivateDNSZoneID)...)
			}
		}
	}
	// No zone group can mean customer-managed DNS. N02 does not prove external forwarding.
	return report
}

func (c *sdkResourceChecker) staticPrivateDNS(ctx context.Context, raw string) Report {
	id, err := existingResourceID(raw, "Microsoft.Network/privateDnsZones")
	if err != nil {
		return Report{readFailure("N02", raw, "Microsoft.Network/privateDnsZones/read", err)}
	}
	zones, err := armprivatedns.NewPrivateZonesClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return Report{readFailure("N02", raw, "Microsoft.Network/privateDnsZones/read", err)}
	}
	zone, err := zones.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return Report{readFailure("N02", raw, "Microsoft.Network/privateDnsZones/read", err)}
	}
	state := ""
	if zone.Properties != nil {
		state = resourceValue(zone.Properties.ProvisioningState)
	}
	report := Report{staticNetworkReadiness(raw, resourceValue(zone.ID), state)}
	links, err := armprivatedns.NewVirtualNetworkLinksClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return append(report, readFailure("N02", raw, "Microsoft.Network/privateDnsZones/virtualNetworkLinks/read", err))
	}
	linkPager := links.NewListPager(id.ResourceGroupName, id.Name, nil)
	for linkPager.More() {
		page, err := linkPager.NextPage(ctx)
		if err != nil {
			return append(report, readFailure("N02", raw, "Microsoft.Network/privateDnsZones/virtualNetworkLinks/read", err))
		}
		for _, link := range page.Value {
			check := resourceCheck("N02", raw, "ready Private DNS VNet link with a complete ARM VNet ID")
			if link == nil || link.Properties == nil || link.Properties.VirtualNetwork == nil {
				report = append(report, check)
				continue
			}
			_, parseErr := existingResourceID(resourceValue(link.Properties.VirtualNetwork.ID), "Microsoft.Network/virtualNetworks")
			report = append(report, resourceKnown(check, link.Properties.ProvisioningState != nil,
				parseErr == nil && resourceValue(link.Properties.ProvisioningState) == "Succeeded",
				"Private DNS VNet link ARM configuration"))
		}
	}
	records, err := armprivatedns.NewRecordSetsClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return append(report, readFailure("N02", raw, "Microsoft.Network/privateDnsZones/ALL/read", err))
	}
	recordPager := records.NewListPager(id.ResourceGroupName, id.Name, nil)
	for recordPager.More() {
		page, err := recordPager.NextPage(ctx)
		if err != nil {
			return append(report, readFailure("N02", raw, "Microsoft.Network/privateDnsZones/ALL/read", err))
		}
		for _, record := range page.Value {
			check := resourceCheck("N02", raw, "readable Private DNS record name, type and TTL")
			known := record != nil && record.Name != nil && record.Type != nil &&
				record.Properties != nil && record.Properties.TTL != nil
			if !known {
				report = append(report, check)
				continue
			}
			switch {
			case strings.EqualFold(*record.Type, "Microsoft.Network/privateDnsZones/A"):
				known = len(record.Properties.ARecords) > 0
			case strings.EqualFold(*record.Type, "Microsoft.Network/privateDnsZones/AAAA"):
				known = len(record.Properties.AaaaRecords) > 0
			case strings.EqualFold(*record.Type, "Microsoft.Network/privateDnsZones/CNAME"):
				known = record.Properties.CnameRecord != nil && record.Properties.CnameRecord.Cname != nil
			}
			if !known {
				report = append(report, resourceResult(check, Unverified, "Private DNS record data is unavailable"))
				continue
			}
			valid := *record.Name != "" && *record.Properties.TTL >= 0
			for _, address := range record.Properties.ARecords {
				if address == nil {
					valid = false
					continue
				}
				ip, err := netip.ParseAddr(resourceValue(address.IPv4Address))
				valid = valid && err == nil && ip.Is4()
			}
			for _, address := range record.Properties.AaaaRecords {
				if address == nil {
					valid = false
					continue
				}
				ip, err := netip.ParseAddr(resourceValue(address.IPv6Address))
				valid = valid && err == nil && ip.Is6()
			}
			report = append(report, resourceKnown(check, true, valid,
				"Private DNS record ARM configuration"))
		}
	}
	return report
}
