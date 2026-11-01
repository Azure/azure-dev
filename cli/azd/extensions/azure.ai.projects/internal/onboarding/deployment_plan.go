// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"fmt"
	"slices"
	"strings"
)

// LegacyNetwork describes selections of the existing standard-template network module.
type LegacyNetwork struct {
	Enabled           bool
	VNetID            string
	AgentSubnetName   string
	PESubnetName      string
	PESubnetPrefix    string
	CreateAgentSubnet bool
	CreatePESubnet    bool
	ManagedEgress     bool
	IsolationMode     string
	DNSResourceGroup  string
	DNSSubscription   string
}

// DeploymentSelection contains effective network selections from the standard Foundry template.
type DeploymentSelection struct {
	Network *LegacyNetwork
}

// AddOperation adds one identified action/scope using the preflight's existing deduplication contract.
func (plan *Plan) AddOperation(checkID, action, scope string) {
	op := operation{checkID, action, scope}
	if !slices.ContainsFunc(plan.Operations, func(existing operation) bool {
		return existing.checkID == op.checkID && strings.EqualFold(existing.action, op.action) &&
			strings.EqualFold(existing.scope, op.scope)
	}) {
		plan.Operations = append(plan.Operations, op)
	}
}

// WithDeploymentOperations binds selected standard-network resource IDs and permission scopes.
func WithDeploymentOperations(plan Plan, selected DeploymentSelection) (Plan, error) {
	plan.Operations = slices.Clone(plan.Operations)
	plan.LegacyNetwork = selected.Network
	n := selected.Network
	if n == nil || !n.Enabled {
		return plan, nil
	}
	if _, err := existingResourceID(n.VNetID, "Microsoft.Network/virtualNetworks"); err != nil {
		return plan, fmt.Errorf("P03: selected network VNet scope: %w", err)
	}
	if !n.ManagedEgress {
		id := n.VNetID + "/subnets/" + n.AgentSubnetName
		subnet, err := existingResourceID(id, "Microsoft.Network/virtualNetworks/subnets")
		if err != nil {
			return plan, fmt.Errorf("N02: selected network.agentSubnet: %w", err)
		}
		plan.AgentSubnet = Resource{ID: id, Parent: subnet.Parent.String(), Supplied: !n.CreateAgentSubnet}
	}
	if _, err := existingResourceID(n.VNetID+"/subnets/"+n.PESubnetName,
		"Microsoft.Network/virtualNetworks/subnets"); err != nil {
		return plan, fmt.Errorf("P03: selected private-endpoint subnet scope: %w", err)
	}
	target := "/subscriptions/" + plan.SubscriptionID
	if plan.ResourceGroupSupplied {
		target += "/resourceGroups/" + plan.ResourceGroup
	}
	appendOperation := func(action, scope string) {
		op := operation{"P03", action, scope}
		if !slices.ContainsFunc(plan.Operations, func(existing operation) bool {
			return existing.checkID == op.checkID && strings.EqualFold(existing.action, op.action) &&
				strings.EqualFold(existing.scope, op.scope)
		}) {
			plan.Operations = append(plan.Operations, op)
		}
	}
	appendOperation("Microsoft.Network/virtualNetworks/read", n.VNetID)
	if n.CreateAgentSubnet || n.CreatePESubnet {
		appendOperation("Microsoft.Network/virtualNetworks/subnets/write", n.VNetID)
	}
	peScope := n.VNetID + "/subnets/" + n.PESubnetName
	if n.CreatePESubnet {
		peScope = n.VNetID
	}
	appendOperation("Microsoft.Network/virtualNetworks/subnets/join/action", peScope)
	if !n.ManagedEgress {
		agentScope := n.VNetID + "/subnets/" + n.AgentSubnetName
		if n.CreateAgentSubnet {
			agentScope = n.VNetID
		}
		if !strings.EqualFold(agentScope, peScope) {
			appendOperation("Microsoft.Network/virtualNetworks/subnets/join/action", agentScope)
		}
	}
	appendOperation("Microsoft.Network/privateEndpoints/write", target)
	appendOperation("Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write", target)
	if n.DNSResourceGroup == "" {
		appendOperation("Microsoft.Network/virtualNetworks/join/action", n.VNetID)
		appendOperation("Microsoft.Network/privateDnsZones/write", target)
		appendOperation("Microsoft.Network/privateDnsZones/virtualNetworkLinks/write", target)
		appendOperation("Microsoft.Network/privateDnsZones/join/action", target)
	} else {
		subscription := n.DNSSubscription
		if subscription == "" {
			subscription = plan.SubscriptionID
		}
		for _, zone := range []string{
			"privatelink.services.ai.azure.com",
			"privatelink.openai.azure.com",
			"privatelink.cognitiveservices.azure.com",
		} {
			scope := "/subscriptions/" + subscription + "/resourceGroups/" + n.DNSResourceGroup +
				"/providers/Microsoft.Network/privateDnsZones/" + zone
			if _, err := existingResourceID(scope, "Microsoft.Network/privateDnsZones"); err != nil {
				return plan, fmt.Errorf("P03: selected DNS zone scope: %w", err)
			}
			appendOperation("Microsoft.Network/privateDnsZones/join/action", scope)
		}
	}
	return plan, nil
}
