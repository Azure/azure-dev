// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
)

func sameSubnetPreparation(a, b SubnetPreparation) bool {
	return strings.EqualFold(a.VNetID, b.VNetID) && strings.EqualFold(a.SubnetID, b.SubnetID) &&
		a.AddressPrefix == b.AddressPrefix && strings.EqualFold(a.Location, b.Location)
}

func selectedSubnetOperation(plan Plan, op Operation) bool {
	if plan.AgentSubnet.Supplied && op.CheckID == "P09" &&
		strings.EqualFold(op.Scope, plan.AgentSubnet.ID) &&
		strings.EqualFold(op.Action, "Microsoft.Network/virtualNetworks/subnets/join/action") {
		return true
	}
	if plan.Network == nil || op.CheckID != "P09" || !strings.EqualFold(op.Scope, plan.Network.VNetID) {
		return false
	}
	return strings.EqualFold(op.Action, "Microsoft.Network/virtualNetworks/subnets/write") ||
		strings.EqualFold(op.Action, "Microsoft.Network/virtualNetworks/subnets/join/action")
}

func (p *ownedPreparer) prepareNetwork(ctx context.Context, plan Plan, state *PreparationState) error {
	if plan.AgentSubnet.Supplied {
		return nil
	}
	n := plan.Network
	if n == nil || state.Network == nil || !sameSubnetPreparation(*n, *state.Network) ||
		!strings.EqualFold(n.SubnetID, plan.AgentSubnet.ID) {
		return fmt.Errorf("R06: a journaled, matching delegated-subnet plan is required")
	}
	id, err := existingResourceID(n.SubnetID, "Microsoft.Network/virtualNetworks/subnets")
	if err != nil || !strings.EqualFold(id.Parent.String(), n.VNetID) {
		return fmt.Errorf("R06: default subnet and VNet IDs do not match")
	}
	parentID, err := arm.ParseResourceID(n.VNetID)
	if err != nil {
		return fmt.Errorf("R06: invalid default VNet: %w", err)
	}
	networks, err := armnetwork.NewVirtualNetworksClient(id.SubscriptionID, p.credential, p.options)
	if err != nil {
		return err
	}
	parent, err := networks.Get(ctx, id.ResourceGroupName, parentID.Name, nil)
	if err != nil {
		return fmt.Errorf("R06: recheck existing VNet before subnet creation: %w", err)
	}
	if parent.Properties == nil || !strings.EqualFold(resourceValue(parent.Location), n.Location) ||
		!strings.EqualFold(resourceValue(parent.ID), n.VNetID) ||
		resourceValue(parent.Properties.ProvisioningState) != "Succeeded" {
		return fmt.Errorf("R06: existing VNet readiness, ID or location changed")
	}
	subnets, err := armnetwork.NewSubnetsClient(id.SubscriptionID, p.credential, p.options)
	if err != nil {
		return err
	}
	response, err := subnets.Get(ctx, id.ResourceGroupName, parentID.Name, id.Name, nil)
	if responseNotFound(err) {
		if err := p.resourceIntent(ctx, state, "R06Subnet", n.SubnetID); err != nil {
			return err
		}
		if err := validateDefaultPrefix(parent.VirtualNetwork, *n, false); err != nil {
			return err
		}
		poller, createErr := subnets.BeginCreateOrUpdate(ctx, id.ResourceGroupName, parentID.Name, id.Name,
			armnetwork.Subnet{Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: new(n.AddressPrefix),
				Delegations: []*armnetwork.Delegation{{
					Name: new("Microsoft.App.environments"),
					Properties: &armnetwork.ServiceDelegationPropertiesFormat{
						ServiceName: new("Microsoft.App/environments"),
					},
				}},
			}}, nil)
		if createErr != nil {
			return fmt.Errorf("R06: create planned delegated subnet: %w", createErr)
		}
		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			return fmt.Errorf("R06: wait for planned subnet; retain the ownership journal: %w", err)
		}
		response, err = subnets.Get(ctx, id.ResourceGroupName, parentID.Name, id.Name, nil)
	}
	if err != nil {
		return fmt.Errorf("R06: read planned delegated subnet: %w", err)
	}
	record, tracked := state.Resources["R06Subnet"]
	if !tracked || !strings.EqualFold(record.ID, n.SubnetID) {
		return fmt.Errorf("R06: existing subnet is not journaled; refusing adoption")
	}
	properties := response.Properties
	if !strings.EqualFold(resourceValue(response.ID), n.SubnetID) || properties == nil ||
		resourceValue(properties.ProvisioningState) != "Succeeded" ||
		resourceValue(properties.AddressPrefix) != n.AddressPrefix ||
		len(properties.AddressPrefixes) != 0 ||
		properties.NetworkSecurityGroup != nil || properties.RouteTable != nil ||
		len(properties.Delegations) != 1 ||
		!slices.ContainsFunc(properties.Delegations, func(d *armnetwork.Delegation) bool {
			return d != nil && d.Properties != nil &&
				resourceValue(d.Properties.ServiceName) == "Microsoft.App/environments"
		}) {
		return fmt.Errorf("R06: subnet readback differs from the exact journaled CIDR/delegation contract")
	}
	if err := validateDefaultPrefix(parent.VirtualNetwork, *n, true); err != nil {
		return fmt.Errorf("R06: recheck current parent address space before accepting the owned subnet: %w", err)
	}
	return p.resourceComplete(ctx, state, "R06Subnet", n.SubnetID)
}
