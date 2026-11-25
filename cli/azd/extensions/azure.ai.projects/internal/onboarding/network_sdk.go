// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore armnetwork

package onboarding

import (
	"context"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
)

func (c *sdkResourceChecker) readNetworkSubnet(ctx context.Context, raw, checkID string) (*armnetwork.Subnet, Report) {
	id, err := existingResourceID(raw, "Microsoft.Network/virtualNetworks/subnets")
	if err != nil {
		check := resourceResult(resourceCheck(checkID, raw, networkResourceType), Fail, "Subnet resource ID is invalid")
		check.Cause = err
		return nil, Report{check}
	}
	client, err := c.clients().subnets.get(strings.ToLower(id.SubscriptionID),
		func() (*armnetwork.SubnetsClient, error) {
			return armnetwork.NewSubnetsClient(id.SubscriptionID, c.credential, c.options)
		})
	if err != nil {
		return nil, Report{readFailure(checkID, raw, networkResourceType, "Subnet", err)}
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
	if err != nil {
		return nil, Report{readFailure(checkID, raw, networkResourceType, "Subnet", err)}
	}
	returnedID := resourceValue(response.ID)
	check := resourceKnown(resourceCheck(checkID, raw, networkResourceType,
		"Returned subnet resource identity could not be verified"),
		returnedID != "", strings.EqualFold(raw, returnedID), "Returned subnet does not match the selected resource")
	if check.Status != Pass {
		return nil, Report{check}
	}
	return &response.Subnet, nil
}

func (c *sdkResourceChecker) readParentNetwork(ctx context.Context, raw string) (*armnetwork.VirtualNetwork, Report) {
	id, err := existingResourceID(raw, "Microsoft.Network/virtualNetworks/subnets")
	if err != nil {
		return nil, nil
	}
	client, err := c.clients().networks.get(strings.ToLower(id.SubscriptionID),
		func() (*armnetwork.VirtualNetworksClient, error) {
			return armnetwork.NewVirtualNetworksClient(id.SubscriptionID, c.credential, c.options)
		})
	if err != nil {
		return nil, Report{readFailure("agent-subnet", id.Parent.String(),
			networkResourceType, "Parent virtual network", err)}
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, nil)
	if err != nil {
		return nil, Report{readFailure("agent-subnet", id.Parent.String(),
			networkResourceType, "Parent virtual network", err)}
	}
	state := ""
	if response.Properties != nil {
		state = resourceValue(response.Properties.ProvisioningState)
	}
	check := resourceReadiness(id.Parent.String(), resourceValue(response.ID), state, networkResourceType)
	check.ID = "agent-subnet"
	return &response.VirtualNetwork, Report{check}
}
