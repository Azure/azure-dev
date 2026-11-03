// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
)

func (c *sdkResourceChecker) readNetworkSubnet(ctx context.Context, raw, checkID string) (*armnetwork.Subnet, Report) {
	id, err := existingResourceID(raw, "Microsoft.Network/virtualNetworks/subnets")
	if err != nil {
		check := resourceResult(resourceCheck(checkID, raw, "complete subnet resource ID"), Fail, "invalid subnet ID")
		check.Cause = err
		return nil, Report{check}
	}
	client, err := c.clients().subnets.get(strings.ToLower(id.SubscriptionID),
		func() (*armnetwork.SubnetsClient, error) {
			return armnetwork.NewSubnetsClient(id.SubscriptionID, c.credential, c.options)
		})
	if err != nil {
		return nil, Report{readFailure(checkID, raw, "Microsoft.Network/virtualNetworks/subnets/read", err)}
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
	if err != nil {
		return nil, Report{readFailure(checkID, raw, "Microsoft.Network/virtualNetworks/subnets/read", err)}
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
	check := resourceReadiness(id.Parent.String(), resourceValue(response.ID), state, "ARM network configuration")
	check.ID = "N02"
	return &response.VirtualNetwork, Report{check}
}
