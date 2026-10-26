// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type networkResourceID struct {
	ID string `json:"id"`
}

type cleanupNetworkProperties struct {
	ResourceGUID                  string               `json:"resourceGuid"`
	ProvisioningState             string               `json:"provisioningState"`
	Subnet                        *networkResourceID   `json:"subnet"`
	PrivateEndpoint               *networkResourceID   `json:"privateEndpoint"`
	VirtualNetwork                *networkResourceID   `json:"virtualNetwork"`
	RegistrationEnabled           *bool                `json:"registrationEnabled"`
	NetworkInterfaces             []*networkResourceID `json:"networkInterfaces"`
	PrivateLinkServiceConnections []struct {
		Properties *struct {
			PrivateLinkServiceID string   `json:"privateLinkServiceId"`
			GroupIDs             []string `json:"groupIds"`
		} `json:"properties"`
	} `json:"privateLinkServiceConnections"`
	ManualPrivateLinkServiceConnections []json.RawMessage `json:"manualPrivateLinkServiceConnections"`
	PrivateDNSZoneConfigs               []struct {
		Properties *struct {
			PrivateDNSZoneID string `json:"privateDnsZoneId"`
			RecordSets       []struct {
				RecordSetName string   `json:"recordSetName"`
				RecordType    string   `json:"recordType"`
				IPAddresses   []string `json:"ipAddresses"`
			} `json:"recordSets"`
		} `json:"properties"`
	} `json:"privateDnsZoneConfigs"`
	IPConfigurations []struct {
		Properties *struct {
			Subnet           *networkResourceID `json:"subnet"`
			PrivateIPAddress string             `json:"privateIPAddress"`
		} `json:"properties"`
	} `json:"ipConfigurations"`
}

func networkAPIVersion(kind string) string {
	if kind == privateZoneType || kind == privateLinkType {
		return "2024-06-01"
	}
	return "2024-05-01"
}

func networkPrivateEndpointID(state *PreparationState) string {
	return "/subscriptions/" + state.SubscriptionID + "/resourceGroups/" + state.ResourceGroup +
		"/providers/Microsoft.Network/privateEndpoints/" + PlannedFoundryAccount(Plan{
		Owner: state.Owner, SubscriptionID: state.SubscriptionID, ResourceGroup: state.ResourceGroup,
	}).Name + "-private-endpoint"
}

func networkZoneIDs(state *PreparationState) []string {
	n := state.Foundry.Network
	group, subscription := state.ResourceGroup, state.SubscriptionID
	if n.DNSResourceGroup != "" {
		group = n.DNSResourceGroup
		if n.DNSSubscription != "" {
			subscription = n.DNSSubscription
		}
	}
	var result []string
	for _, name := range []string{
		"privatelink.services.ai.azure.com", "privatelink.openai.azure.com", "privatelink.cognitiveservices.azure.com",
	} {
		result = append(result, "/subscriptions/"+subscription+"/resourceGroups/"+group+
			"/providers/Microsoft.Network/privateDnsZones/"+name)
	}
	return result
}

func containsResourceID(ids []string, id string) bool {
	return slices.ContainsFunc(ids, func(expected string) bool { return strings.EqualFold(expected, id) })
}

func (l *ownedLifecycle) readNetworkResource(
	ctx context.Context, plan Plan, id, kind string,
) (armresources.GenericResource, error) {
	client, err := armresources.NewClient(plan.SubscriptionID, l.credential, l.options)
	if err != nil {
		return armresources.GenericResource{}, err
	}
	found, err := client.GetByID(ctx, id, networkAPIVersion(kind), nil)
	if err != nil {
		return found.GenericResource, err
	}
	if !strings.EqualFold(authString(found.ID), id) {
		return found.GenericResource, fmt.Errorf("network resource response ID differs from its exact deployment target")
	}
	return found.GenericResource, nil
}

func (l *ownedLifecycle) observeNetworkTarget(
	ctx context.Context, plan Plan, state *PreparationState, id, kind string,
	windows []networkBirthWindow, expanded map[string]*armresources.GenericResourceExpanded,
) error {
	network := state.Foundry.Network
	id = strings.ToLower(id)
	parsed, err := ParseID(id, kind)
	if err != nil {
		return err
	}
	if !strings.EqualFold(parsed.SubscriptionID, plan.SubscriptionID) ||
		!strings.EqualFold(parsed.ResourceGroupName, plan.ResourceGroup) {
		return fmt.Errorf("network cleanup target is outside the recorded resource group")
	}
	zones, pe := networkZoneIDs(state), networkPrivateEndpointID(state)
	parent := ""
	switch kind {
	case privateEndpointType:
		if !strings.EqualFold(id, pe) {
			return fmt.Errorf("network deployment contains an unexpected private endpoint")
		}
	case privateZoneGroupType:
		account, err := ParseID(state.Foundry.Spec.AccountID, "Microsoft.CognitiveServices/accounts")
		if err != nil {
			return err
		}
		if !strings.EqualFold(id, pe+"/privateDnsZoneGroups/"+account.Name+"-dns-group") {
			return fmt.Errorf("network deployment contains an unexpected DNS zone group")
		}
		parent = pe
	case privateZoneType:
		if network.DNSResourceGroup != "" || !containsResourceID(zones, id) {
			return fmt.Errorf("refusing ownership of a supplied or unexpected private DNS zone")
		}
	case privateLinkType:
		if network.DNSResourceGroup != "" || !containsResourceID(zones, parsed.Parent.String()) {
			return fmt.Errorf("refusing ownership of a supplied or unexpected private DNS zone link")
		}
	case privateNICType:
		parent = pe
	default:
		return fmt.Errorf("unsupported network cleanup resource type")
	}
	found, err := l.readNetworkResource(ctx, plan, id, kind)
	record, recorded := network.Resources[id]
	if responseNotFound(err) {
		record.ID, record.Type, record.Deleted = id, kind, true
		network.Resources[id] = record
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify exact network cleanup target %s: %w", id, err)
	}
	if recorded && record.Deleted {
		return fmt.Errorf("previously deleted network resource %s was recreated; refusing deletion", id)
	}
	actual := expanded[id]
	if kind == privateZoneGroupType {
		actual = expanded[strings.ToLower(parent)]
	}
	if err := proveNetworkBirth(id, actual, windows, parent, kind == privateNICType); err != nil {
		return err
	}
	if recorded && !record.CreatedTime.IsZero() && !record.CreatedTime.Equal(*actual.CreatedTime) {
		return fmt.Errorf("network resource %s creation identity changed; refusing deletion", id)
	}
	if owner, present := found.Tags[preparationOwnerTag]; present && authString(owner) != state.Owner {
		return fmt.Errorf("network resource %s has a conflicting owner", id)
	}
	if err := verifyNetworkResource(state, kind, found); err != nil {
		return fmt.Errorf("network resource %s binding is unverified: %w", id, err)
	}
	network.Resources[id] = FoundryNetworkResource{
		ID: id, Type: kind, BirthResourceID: authString(actual.ID), CreatedTime: *actual.CreatedTime,
	}
	if kind == privateEndpointType {
		p, err := decodeNetworkProperties(found)
		if err != nil {
			return err
		}
		for _, nic := range p.NetworkInterfaces {
			if err := l.observeNetworkTarget(ctx, plan, state, nic.ID, privateNICType, windows, expanded); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeNetworkProperties(found armresources.GenericResource) (cleanupNetworkProperties, error) {
	var result cleanupNetworkProperties
	raw, err := json.Marshal(found.Properties)
	if err == nil {
		err = json.Unmarshal(raw, &result)
	}
	if err != nil {
		return result, fmt.Errorf("decode network resource evidence: %w", err)
	}
	if result.ProvisioningState != "Succeeded" && result.ProvisioningState != "Deleting" {
		return result, fmt.Errorf("network provisioning state is unverified")
	}
	return result, nil
}

func verifyNetworkResource(state *PreparationState, kind string, found armresources.GenericResource) error {
	p, err := decodeNetworkProperties(found)
	if err != nil {
		return err
	}
	n, pe := state.Foundry.Network, networkPrivateEndpointID(state)
	switch kind {
	case privateEndpointType:
		if !strings.EqualFold(authString(found.Location), state.Location) ||
			p.Subnet == nil || !strings.EqualFold(p.Subnet.ID, n.PESubnetID) ||
			len(p.ManualPrivateLinkServiceConnections) != 0 || len(p.PrivateLinkServiceConnections) != 1 ||
			p.PrivateLinkServiceConnections[0].Properties == nil || len(p.NetworkInterfaces) != 1 ||
			p.NetworkInterfaces[0] == nil || p.NetworkInterfaces[0].ID == "" {
			return fmt.Errorf("private endpoint subnet, location, connections or interface differs")
		}
		connection := p.PrivateLinkServiceConnections[0].Properties
		if !strings.EqualFold(connection.PrivateLinkServiceID, state.Foundry.Spec.AccountID) ||
			len(connection.GroupIDs) != 1 || connection.GroupIDs[0] != "account" {
			return fmt.Errorf("private endpoint does not exclusively bind the exact owned Foundry account")
		}
	case privateNICType:
		if p.PrivateEndpoint == nil || !strings.EqualFold(p.PrivateEndpoint.ID, pe) ||
			!strings.EqualFold(authString(found.ManagedBy), pe) || len(p.IPConfigurations) == 0 {
			return fmt.Errorf("network interface is not managed by the exact owned private endpoint")
		}
		for _, ip := range p.IPConfigurations {
			if ip.Properties == nil || ip.Properties.Subnet == nil ||
				!strings.EqualFold(ip.Properties.Subnet.ID, n.PESubnetID) || ip.Properties.PrivateIPAddress == "" {
				return fmt.Errorf("private endpoint interface subnet or IP configuration differs")
			}
		}
	case privateLinkType:
		if p.VirtualNetwork == nil || !strings.EqualFold(p.VirtualNetwork.ID, n.VNetID) ||
			p.RegistrationEnabled == nil || *p.RegistrationEnabled {
			return fmt.Errorf("DNS link VNet or registration setting differs")
		}
	case privateZoneGroupType:
		zones := networkZoneIDs(state)
		if len(p.PrivateDNSZoneConfigs) != len(zones) {
			return fmt.Errorf("private DNS zone group does not bind exactly the three selected zones")
		}
		seen := map[string]bool{}
		for _, config := range p.PrivateDNSZoneConfigs {
			if config.Properties == nil || !containsResourceID(zones, config.Properties.PrivateDNSZoneID) ||
				seen[strings.ToLower(config.Properties.PrivateDNSZoneID)] {
				return fmt.Errorf("private DNS zone group contains an unexpected or duplicate zone")
			}
			seen[strings.ToLower(config.Properties.PrivateDNSZoneID)] = true
		}
	case privateZoneType:
		if !strings.EqualFold(authString(found.Location), "global") {
			return fmt.Errorf("private DNS zone location differs")
		}
	}
	return nil
}

func (l *ownedLifecycle) cleanupFoundryNetwork(
	ctx context.Context, plan Plan, state *PreparationState, progress func(string),
) error {
	if state.Foundry == nil || state.Foundry.Network == nil {
		return nil
	}
	if err := l.inventoryFoundryNetwork(ctx, plan, state); err != nil {
		return err
	}
	client, err := armresources.NewClient(plan.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	for _, kind := range []string{privateZoneGroupType, privateEndpointType, privateNICType, privateLinkType, privateZoneType} {
		var ids []string
		for id, resource := range state.Foundry.Network.Resources {
			if resource.Type == kind && !resource.Deleted {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := l.inventoryFoundryNetwork(ctx, plan, state); err != nil {
				return err
			}
			if state.Foundry.Network.Resources[id].Deleted {
				continue
			}
			observed, err := l.readNetworkResource(ctx, plan, id, kind)
			if !responseNotFound(err) {
				if err != nil {
					return err
				}
				if kind == privateNICType {
					return fmt.Errorf("RP-owned private endpoint NIC deletion is not confirmed; no direct NIC delete is permitted")
				}
				if err := verifyNetworkResource(state, kind, observed); err != nil {
					return err
				}
				if kind == privateZoneType {
					if err := l.verifyNetworkZoneChildren(ctx, plan, state, id); err != nil {
						return err
					}
				}
				if progress != nil {
					progress(fmt.Sprintf("Deleting owned network resource %s...", id))
				}
				poller, err := client.BeginDeleteByID(ctx, id, networkAPIVersion(kind), nil)
				if err != nil && !responseNotFound(err) {
					return fmt.Errorf("delete owned network resource %s: %w", id, err)
				}
				if err == nil {
					if _, err := poller.PollUntilDone(ctx, nil); err != nil {
						return err
					}
				}
				if _, err := l.readNetworkResource(ctx, plan, id, kind); !responseNotFound(err) {
					if err != nil {
						return err
					}
					return fmt.Errorf("owned network resource %s deletion is not confirmed", id)
				}
			}
			record := state.Foundry.Network.Resources[id]
			record.Deleted = true
			state.Foundry.Network.Resources[id] = record
			if err := l.journal.Save(ctx, *state); err != nil {
				return err
			}
		}
	}
	return nil
}
