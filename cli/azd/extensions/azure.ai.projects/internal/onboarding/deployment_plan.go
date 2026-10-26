// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
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

// ValidateHostingNetwork requires the standard non-managed subnet injection supported by AKS hosting.
func ValidateHostingNetwork(selected DeploymentSelection) error {
	n := selected.Network
	if n == nil || !n.Enabled || n.ManagedEgress {
		return fmt.Errorf("BYO AKS agentHosting requires network.agentSubnet and network.peSubnet; " +
			"Microsoft-managed egress and an omitted network cannot host this configuration")
	}
	if err := validateLegacyNetwork(Plan{LegacyNetwork: n}); err != nil {
		return err
	}
	if strings.EqualFold(n.AgentSubnetName, n.PESubnetName) {
		return fmt.Errorf("network.agentSubnet.name and network.peSubnet.name must differ")
	}
	return nil
}

// ResolveRegistrySelection distinguishes customer input from an owned registry's persisted environment output.
func ResolveRegistrySelection(ctx context.Context, plan Plan, journal Journal) (Plan, error) {
	if !plan.RegistryCustomerSupplied {
		return plan, nil
	}
	state, err := (&ownedLifecycle{journal: journal}).load(ctx, plan)
	if err != nil {
		return plan, fmt.Errorf("I03: resolve original registry selection: %w", err)
	}
	if record, ok := state.Resources["R03"]; ok && !state.CleanupComplete && !record.Deleted &&
		strings.EqualFold(record.ID, plan.Registry.ID) {
		plan.RegistryCustomerSupplied = false
	}
	return plan, nil
}

// WithDeploymentOperations binds network inputs and only BYO-added cross-RG deployment permissions.
func WithDeploymentOperations(plan Plan, selected DeploymentSelection) (Plan, error) {
	plan.Operations = slices.Clone(plan.Operations)
	plan.LegacyNetwork = selected.Network
	if err := validateLegacyNetwork(plan); err != nil {
		return plan, err
	}
	if n := selected.Network; n != nil && n.Enabled && !n.ManagedEgress {
		id := n.VNetID + "/subnets/" + n.AgentSubnetName
		subnet, err := ParseID(id, "Microsoft.Network/virtualNetworks/subnets")
		if err != nil {
			return plan, fmt.Errorf("I03: selected network.agentSubnet: %w", err)
		}
		plan.AgentSubnet = Resource{
			ID: id, Name: subnet.Name, Parent: n.VNetID, Supplied: !n.CreateAgentSubnet,
		}
	}
	if n := selected.Network; n != nil && n.Enabled {
		target := "/subscriptions/" + plan.SubscriptionID
		if plan.ResourceGroupSupplied {
			target += "/resourceGroups/" + plan.ResourceGroup
		}
		appendOperation := func(action, scope string) {
			plan.Operations = append(plan.Operations, Operation{CheckID: "P03", Action: action, Scope: scope})
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
				appendOperation("Microsoft.Network/privateDnsZones/join/action", scope)
			}
		}
	}
	target := "/subscriptions/" + plan.SubscriptionID + "/resourceGroups/" + plan.ResourceGroup
	for _, item := range []struct {
		resource Resource
		kind     string
		selected bool
	}{
		{plan.Cluster, "Microsoft.ContainerService/managedClusters", createsGrant(plan, "G01")},
		{plan.Workload, "Microsoft.ManagedIdentity/userAssignedIdentities", createsGrant(plan, "G02")},
		{plan.Storage, "Microsoft.Storage/storageAccounts", createsGrant(plan, "G03")},
		{plan.Registry, "Microsoft.ContainerRegistry/registries", true},
	} {
		if !item.selected {
			continue
		}
		id, err := ParseID(item.resource.ID, item.kind)
		if err != nil {
			return plan, fmt.Errorf("P09: BYO nested deployment scope: %w", err)
		}
		scope := id.Parent.String()
		if strings.EqualFold(scope, target) {
			continue
		}
		for _, action := range []string{"write", "read", "operations/read"} {
			op := Operation{CheckID: "P09", Scope: scope, Action: "Microsoft.Resources/deployments/" + action}
			if !slices.ContainsFunc(plan.Operations, func(existing Operation) bool {
				return existing.CheckID == op.CheckID && strings.EqualFold(existing.Scope, op.Scope) &&
					strings.EqualFold(existing.Action, op.Action)
			}) {
				plan.Operations = append(plan.Operations, op)
			}
		}
	}
	return plan, nil
}

func validateLegacyNetwork(plan Plan) error {
	n := plan.LegacyNetwork
	if n == nil || !n.Enabled {
		return nil
	}
	if _, err := ParseID(n.VNetID, "Microsoft.Network/virtualNetworks"); err != nil {
		return fmt.Errorf("I03: selected legacy network VNet: %w", err)
	}
	for _, name := range []string{n.PESubnetName, n.AgentSubnetName} {
		if name == "" || strings.ContainsAny(name, "/\\?#%${} \t\r\n") {
			return fmt.Errorf("I03: selected legacy network subnet name is invalid")
		}
	}
	if n.DNSResourceGroup != "" {
		subscription := n.DNSSubscription
		if subscription == "" {
			subscription = plan.SubscriptionID
		}
		for _, zone := range []string{
			"privatelink.services.ai.azure.com", "privatelink.openai.azure.com", "privatelink.cognitiveservices.azure.com",
		} {
			scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/privateDnsZones/%s",
				subscription, n.DNSResourceGroup, zone)
			if _, err := ParseID(scope, "Microsoft.Network/privateDnsZones"); err != nil {
				return fmt.Errorf("I03: selected legacy DNS zone scope: %w", err)
			}
		}
	}
	return nil
}
