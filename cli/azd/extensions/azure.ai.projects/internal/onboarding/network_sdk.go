// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"net/netip"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

func (c *sdkResourceChecker) readNetworkSubnet(ctx context.Context, raw, checkID string) (*armnetwork.Subnet, Report) {
	id, err := existingResourceID(raw, "Microsoft.Network/virtualNetworks/subnets")
	if err != nil {
		check := resourceResult(resourceCheck(checkID, raw, "complete subnet resource ID"), Fail, "invalid subnet ID")
		check.Cause = err
		return nil, Report{check}
	}
	client, err := armnetwork.NewSubnetsClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, Report{readFailure(checkID, raw, "Microsoft.Network/virtualNetworks/subnets/read", err)}
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
	if err != nil {
		return nil, Report{readFailure(checkID, raw, "Microsoft.Network/virtualNetworks/subnets/read", err)}
	}
	state := ""
	if response.Properties != nil {
		state = resourceValue(response.Properties.ProvisioningState)
	}
	report := Report{resourceReadiness(raw, resourceValue(response.ID), state, "subnets/read")}
	if response.Properties == nil {
		return &response.Subnet, report
	}
	if nsg := response.Properties.NetworkSecurityGroup; nsg != nil && nsg.ID == nil && checkID == "N02" {
		report = append(report, resourceCheck(checkID, raw, "associated NSG ARM resource ID"))
	} else if nsg != nil && nsg.ID != nil {
		id, err := existingResourceID(*nsg.ID, "Microsoft.Network/networkSecurityGroups")
		if err == nil {
			client, clientErr := armnetwork.NewSecurityGroupsClient(id.SubscriptionID, c.credential, c.options)
			err = clientErr
			if err == nil {
				response, readErr := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
				err = readErr
				if err == nil && checkID == "N02" {
					report = append(report, staticSecurityGroup(response.SecurityGroup, *nsg.ID))
				}
			}
		}
		if err != nil {
			report = append(report, readFailure(checkID, *nsg.ID, "Microsoft.Network/networkSecurityGroups/read", err))
		}
	}
	if route := response.Properties.RouteTable; route != nil && route.ID == nil && checkID == "N02" {
		report = append(report, resourceCheck(checkID, raw, "associated route-table ARM resource ID"))
	} else if route != nil && route.ID != nil {
		id, err := existingResourceID(*route.ID, "Microsoft.Network/routeTables")
		if err == nil {
			client, clientErr := armnetwork.NewRouteTablesClient(id.SubscriptionID, c.credential, c.options)
			err = clientErr
			if err == nil {
				response, readErr := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
				err = readErr
				if err == nil && checkID == "N02" {
					report = append(report, staticRouteTable(response.RouteTable, *route.ID))
				}
			}
		}
		if err != nil {
			report = append(report, readFailure(checkID, *route.ID, "Microsoft.Network/routeTables/read", err))
		}
	}
	return &response.Subnet, report
}

func (c *sdkResourceChecker) readParentNetwork(ctx context.Context, raw string) (*armnetwork.VirtualNetwork, Report) {
	id, err := existingResourceID(raw, "Microsoft.Network/virtualNetworks/subnets")
	if err != nil {
		return nil, nil
	}
	client, err := armnetwork.NewVirtualNetworksClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return nil, Report{readFailure("N02", id.Parent.String(), "Microsoft.Network/virtualNetworks/read", err)}
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, nil)
	if err != nil {
		return nil, Report{readFailure("N02", id.Parent.String(), "Microsoft.Network/virtualNetworks/read", err)}
	}
	state := ""
	if response.Properties != nil {
		state = resourceValue(response.Properties.ProvisioningState)
	}
	report := Report{staticNetworkReadiness(id.Parent.String(), resourceValue(response.ID), state)}
	if response.Properties != nil && response.Properties.DhcpOptions != nil {
		for _, server := range response.Properties.DhcpOptions.DNSServers {
			check := resourceCheck("N02", id.Parent.String(), "valid ARM custom DNS server address")
			_, parseErr := netip.ParseAddr(resourceValue(server))
			report = append(report, resourceKnown(check, server != nil, parseErr == nil, "ARM custom DNS server configuration"))
		}
	}
	return &response.VirtualNetwork, report
}

func (c *sdkResourceChecker) storagePrivateEndpoints(ctx context.Context, storage *armstorage.Account) Report {
	var report Report
	for _, connection := range storage.Properties.PrivateEndpointConnections {
		if connection == nil || connection.Properties == nil {
			report = append(report, resourceCheck("S03", resourceValue(storage.ID), "readable private endpoint approval"))
			continue
		}
		p := connection.Properties
		check := resourceCheck("S03", resourceValue(connection.ID), "approved existing Storage private endpoint")
		if p.PrivateLinkServiceConnectionState == nil {
			report = append(report, check)
		} else {
			state := resourceValue(p.PrivateLinkServiceConnectionState.Status)
			report = append(report, resourceKnown(check, state != "", state == "Approved", "approval="+state))
		}
		if p.PrivateEndpoint == nil || p.PrivateEndpoint.ID == nil {
			report = append(report, resourceCheck("S03", resourceValue(connection.ID), "private endpoint resource ID"))
			continue
		}
		raw := *p.PrivateEndpoint.ID
		id, err := existingResourceID(raw, "Microsoft.Network/privateEndpoints")
		if err == nil {
			client, clientErr := armnetwork.NewPrivateEndpointsClient(id.SubscriptionID, c.credential, c.options)
			err = clientErr
			if err == nil {
				response, readErr := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
				err = readErr
				if err == nil {
					state := ""
					if response.Properties != nil {
						state = resourceValue(response.Properties.ProvisioningState)
					}
					report = append(report,
						resourceReadiness(raw, resourceValue(response.ID), state, "privateEndpoints/read"))
				}
			}
		}
		if err != nil {
			report = append(report, readFailure("S03", raw, "Microsoft.Network/privateEndpoints/read", err))
		}
		// DNS zone IDs are discovered from the endpoint's zone groups, not guessed from its hostname.
		report = append(report, c.privateEndpointDNS(ctx, raw)...)
	}
	return report
}

func (c *sdkResourceChecker) privateEndpointDNS(ctx context.Context, raw string) Report {
	id, err := existingResourceID(raw, "Microsoft.Network/privateEndpoints")
	if err != nil {
		return nil
	}
	client, err := armnetwork.NewPrivateDNSZoneGroupsClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		return Report{readFailure("S03", raw, "Microsoft.Network/privateEndpoints/privateDnsZoneGroups/read", err)}
	}
	pager := client.NewListPager(id.ResourceGroupName, id.Name, nil)
	var report Report
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return append(report, readFailure("S03", raw,
				"Microsoft.Network/privateEndpoints/privateDnsZoneGroups/read", err))
		}
		for _, group := range page.Value {
			if group == nil || group.Properties == nil {
				continue
			}
			for _, config := range group.Properties.PrivateDNSZoneConfigs {
				if config == nil || config.Properties == nil || config.Properties.PrivateDNSZoneID == nil {
					continue
				}
				found = true
				report = append(report, c.readPrivateDNS(ctx, *config.Properties.PrivateDNSZoneID)...)
			}
		}
	}
	if !found {
		check := resourceCheck("S03", raw, "existing private DNS zone links/records or verified external DNS")
		check.Observed = "no managed private DNS zone association; external DNS forwarding must be verified"
		report = append(report, check)
	}
	return report
}

func (c *sdkResourceChecker) readPrivateDNS(ctx context.Context, raw string) Report {
	id, err := existingResourceID(raw, "Microsoft.Network/privateDnsZones")
	if err != nil {
		return Report{readFailure("S03", raw, "Microsoft.Network/privateDnsZones/read", err)}
	}
	zones, err := armprivatedns.NewPrivateZonesClient(id.SubscriptionID, c.credential, c.options)
	if err == nil {
		_, err = zones.Get(ctx, id.ResourceGroupName, id.Name, nil)
	}
	if err != nil {
		return Report{readFailure("S03", raw, "Microsoft.Network/privateDnsZones/read", err)}
	}
	links, err := armprivatedns.NewVirtualNetworkLinksClient(id.SubscriptionID, c.credential, c.options)
	if err == nil {
		pager := links.NewListPager(id.ResourceGroupName, id.Name, nil)
		for pager.More() {
			_, err = pager.NextPage(ctx)
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		return Report{readFailure("S03", raw, "Microsoft.Network/privateDnsZones/virtualNetworkLinks/read", err)}
	}
	records, err := armprivatedns.NewRecordSetsClient(id.SubscriptionID, c.credential, c.options)
	if err == nil {
		pager := records.NewListPager(id.ResourceGroupName, id.Name, nil)
		for pager.More() {
			_, err = pager.NextPage(ctx)
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		return Report{readFailure("S03", raw, "Microsoft.Network/privateDnsZones/ALL/read", err)}
	}
	return nil // ARM DNS records alone cannot establish workload resolution or forwarding.
}
