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
	target := "/subscriptions/" + plan.SubscriptionID + "/resourceGroups/" + plan.ResourceGroup
	for _, item := range []struct {
		resource Resource
		kind     string
	}{
		{plan.Cluster, "Microsoft.ContainerService/managedClusters"},
		{plan.Storage, "Microsoft.Storage/storageAccounts"},
		{plan.Registry, "Microsoft.ContainerRegistry/registries"},
	} {
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
		if name == "" || strings.ContainsAny(name, "/\\?#%") {
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
