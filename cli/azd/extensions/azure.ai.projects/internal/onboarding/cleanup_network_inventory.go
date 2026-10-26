// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
)

func (l *ownedLifecycle) verifyNetworkTopology(ctx context.Context, plan Plan, state *PreparationState) error {
	for _, resource := range state.Foundry.Network.Resources {
		if resource.Deleted {
			continue
		}
		switch resource.Type {
		case privateEndpointType:
			id, err := ParseID(resource.ID, privateEndpointType)
			if err != nil {
				return err
			}
			client, err := armnetwork.NewPrivateDNSZoneGroupsClient(id.SubscriptionID, l.credential, l.options)
			if err != nil {
				return err
			}
			pager := client.NewListPager(id.Name, id.ResourceGroupName, nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return fmt.Errorf("inventory exact private endpoint DNS groups: %w", err)
				}
				for _, group := range page.Value {
					if group == nil {
						return fmt.Errorf("private endpoint DNS group inventory is incomplete")
					}
					record, known := state.Foundry.Network.Resources[strings.ToLower(authString(group.ID))]
					if !known || record.Type != privateZoneGroupType || record.Deleted {
						return fmt.Errorf("private endpoint contains an untracked or recreated DNS zone group")
					}
				}
			}
		case privateZoneType:
			if err := l.verifyNetworkZoneChildren(ctx, plan, state, resource.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *ownedLifecycle) verifyNetworkZoneChildren(
	ctx context.Context, plan Plan, state *PreparationState, zoneID string,
) error {
	id, err := ParseID(zoneID, privateZoneType)
	if err != nil {
		return err
	}
	links, err := armprivatedns.NewVirtualNetworkLinksClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	pager := links.NewListPager(id.ResourceGroupName, id.Name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("inventory all links before deleting owned DNS zone: %w", err)
		}
		for _, link := range page.Value {
			if link == nil {
				return fmt.Errorf("DNS link inventory is incomplete")
			}
			record, known := state.Foundry.Network.Resources[strings.ToLower(authString(link.ID))]
			if !known || record.Type != privateLinkType || record.Deleted {
				return fmt.Errorf("DNS zone contains an untracked or recreated link; refusing cascading deletion")
			}
			p := link.Properties
			if p == nil || p.VirtualNetwork == nil || p.RegistrationEnabled == nil || *p.RegistrationEnabled ||
				!strings.EqualFold(authString(p.VirtualNetwork.ID), state.Foundry.Network.VNetID) {
				return fmt.Errorf("DNS zone link has a different VNet or registration configuration")
			}
		}
	}
	records, err := armprivatedns.NewRecordSetsClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	recordPager := records.NewListPager(id.ResourceGroupName, id.Name, nil)
	account, err := ParseID(state.Foundry.Spec.AccountID, "Microsoft.CognitiveServices/accounts")
	if err != nil {
		return err
	}
	pe, err := l.readNetworkResource(ctx, plan, networkPrivateEndpointID(state), privateEndpointType)
	peExists := err == nil
	if err != nil && !responseNotFound(err) {
		return err
	}
	var addresses []string
	var zoneAddresses []string
	var peGUID string
	if peExists {
		p, err := decodeNetworkProperties(pe)
		if err != nil {
			return err
		}
		peGUID = p.ResourceGUID
		for _, nic := range p.NetworkInterfaces {
			actual, err := l.readNetworkResource(ctx, plan, nic.ID, privateNICType)
			if err != nil {
				return err
			}
			properties, err := decodeNetworkProperties(actual)
			if err != nil {
				return err
			}
			for _, ip := range properties.IPConfigurations {
				if ip.Properties == nil {
					return fmt.Errorf("private endpoint NIC address evidence is incomplete")
				}
				addresses = append(addresses, ip.Properties.PrivateIPAddress)
			}
		}
		accountID, err := ParseID(state.Foundry.Spec.AccountID, "Microsoft.CognitiveServices/accounts")
		if err != nil {
			return err
		}
		groupID := networkPrivateEndpointID(state) + "/privateDnsZoneGroups/" + accountID.Name + "-dns-group"
		group, err := l.readNetworkResource(ctx, plan, groupID, privateZoneGroupType)
		if err != nil && !responseNotFound(err) {
			return err
		}
		if err == nil {
			config, err := decodeNetworkProperties(group)
			if err != nil {
				return err
			}
			for _, zone := range config.PrivateDNSZoneConfigs {
				if zone.Properties == nil || !strings.EqualFold(zone.Properties.PrivateDNSZoneID, zoneID) {
					continue
				}
				for _, record := range zone.Properties.RecordSets {
					if record.RecordType != "A" || !strings.EqualFold(record.RecordSetName, accountID.Name) {
						return fmt.Errorf("private DNS group record ownership is unverified")
					}
					zoneAddresses = append(zoneAddresses, record.IPAddresses...)
				}
			}
		}
	}
	for recordPager.More() {
		page, err := recordPager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("inventory all records before deleting owned DNS zone: %w", err)
		}
		for _, record := range page.Value {
			if record == nil || record.Properties == nil {
				return fmt.Errorf("DNS record inventory is incomplete")
			}
			if authString(record.Name) == "@" &&
				strings.EqualFold(authString(record.Type), privateZoneType+"/SOA") {
				continue
			}
			p := record.Properties
			if !peExists || !strings.EqualFold(authString(record.Name), account.Name) ||
				!strings.EqualFold(authString(record.Type), privateZoneType+"/A") ||
				len(p.ARecords) == 0 || len(p.ARecords) != len(zoneAddresses) || peGUID == "" ||
				!strings.EqualFold(authString(p.Metadata["creator"]), "created by private endpoint "+
					account.Name+"-private-endpoint with resource guid "+peGUID) {
				return fmt.Errorf("DNS zone contains an untracked record; refusing cascading deletion")
			}
			for _, address := range p.ARecords {
				if address == nil || !containsResourceID(addresses, authString(address.IPv4Address)) ||
					!containsResourceID(zoneAddresses, authString(address.IPv4Address)) {
					return fmt.Errorf("DNS record does not target an actual owned private endpoint address")
				}
			}
		}
	}
	return nil
}

// The service's account endpoint-list contract is not paginated. Decode nextLink
// explicitly rather than letting the pinned SDK silently drop an unknown page.
func (l *ownedLifecycle) inventoryAccountPrivateEndpoints(ctx context.Context, state *PreparationState) error {
	client, err := arm.NewClient("azure.ai.projects.onboarding", "v1.0.0", l.credential, l.options)
	if err != nil {
		return err
	}
	request, err := runtime.NewRequest(ctx, http.MethodGet, strings.TrimRight(client.Endpoint(), "/")+
		state.Foundry.Spec.AccountID+"/privateEndpointConnections?api-version=2025-06-01")
	if err != nil {
		return err
	}
	response, err := client.Pipeline().Do(request)
	if err != nil {
		return fmt.Errorf("inventory account private endpoint connections: %w", err)
	}
	defer response.Body.Close()
	if !runtime.HasStatusCode(response, http.StatusOK) {
		return fmt.Errorf("inventory account private endpoint connections: %w", runtime.NewResponseError(response))
	}
	var page struct {
		Value    []*armcognitiveservices.PrivateEndpointConnection `json:"value"`
		NextLink string                                            `json:"nextLink"`
	}
	if err := runtime.UnmarshalAsJSON(response, &page); err != nil {
		return err
	}
	if page.NextLink != "" {
		return fmt.Errorf("account private endpoint inventory has an unsupported continuation; deletion is blocked")
	}
	if page.Value == nil {
		return fmt.Errorf("account private endpoint inventory has no complete value array; deletion is blocked")
	}
	for _, connection := range page.Value {
		if connection == nil || connection.Properties == nil || connection.Properties.PrivateEndpoint == nil {
			return fmt.Errorf("account private endpoint connection inventory is incomplete")
		}
		id, err := ParseID(authString(connection.ID), "Microsoft.CognitiveServices/accounts/privateEndpointConnections")
		if err != nil || !strings.EqualFold(id.Parent.String(), state.Foundry.Spec.AccountID) {
			return fmt.Errorf("account private endpoint connection identity or parent is unverified")
		}
		p := connection.Properties
		endpointID := networkPrivateEndpointID(state)
		if state.Foundry.Network == nil || !strings.EqualFold(authString(p.PrivateEndpoint.ID), endpointID) {
			return fmt.Errorf("account contains an untracked private endpoint connection; refusing cascading deletion")
		}
		resource, known := state.Foundry.Network.Resources[strings.ToLower(endpointID)]
		if !known || resource.Deleted {
			return fmt.Errorf("account private endpoint connection remains after owned endpoint deletion")
		}
	}
	return nil
}
