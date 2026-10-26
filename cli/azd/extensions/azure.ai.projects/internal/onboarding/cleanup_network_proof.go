// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

const (
	privateEndpointType  = "Microsoft.Network/privateEndpoints"
	privateZoneGroupType = privateEndpointType + "/privateDnsZoneGroups"
	privateZoneType      = "Microsoft.Network/privateDnsZones"
	privateLinkType      = privateZoneType + "/virtualNetworkLinks"
	privateNICType       = "Microsoft.Network/networkInterfaces"
)

// FoundryNetworkState records exact control-plane-proven network targets, not a resource-group delete.
type FoundryNetworkState struct {
	VNetID           string                            `json:"vnetId"`
	PESubnetID       string                            `json:"peSubnetId"`
	DNSResourceGroup string                            `json:"dnsResourceGroup,omitempty"`
	DNSSubscription  string                            `json:"dnsSubscription,omitempty"`
	Resources        map[string]FoundryNetworkResource `json:"resources"`
}

// FoundryNetworkResource records the exact birth-evidence source and durable delete tombstone.
type FoundryNetworkResource struct {
	ID              string    `json:"id"`
	Type            string    `json:"type"`
	BirthResourceID string    `json:"birthResourceId,omitempty"`
	CreatedTime     time.Time `json:"createdTime"`
	Deleted         bool      `json:"deleted,omitempty"`
}

type networkBirthWindow struct {
	start, end time.Time
	outputs    map[string]bool
	network    FoundryNetworkState
}

func networkCleanupType(kind string) bool {
	for _, allowed := range []string{privateEndpointType, privateZoneGroupType, privateZoneType, privateLinkType} {
		if strings.EqualFold(kind, allowed) {
			return true
		}
	}
	return false
}

// Deployment timestamps describe terminal updates. Duration measures backwards
// from that timestamp, not forwards. ChangedTime is never creation evidence.
func networkWindow(properties *armresources.DeploymentPropertiesExtended) (networkBirthWindow, error) {
	var result networkBirthWindow
	raw, err := json.Marshal(properties.Parameters)
	if err != nil {
		return result, err
	}
	var parameters struct {
		Enabled struct {
			Value bool `json:"value"`
		} `json:"enableNetworkIsolation"`
		VNet struct {
			Value string `json:"value"`
		} `json:"vnetId"`
		Subnet struct {
			Value string `json:"value"`
		} `json:"peSubnetName"`
		DNSGroup struct {
			Value string `json:"value"`
		} `json:"dnsZonesResourceGroup"`
		DNSSubscription struct {
			Value string `json:"value"`
		} `json:"dnsZonesSubscription"`
	}
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return result, fmt.Errorf("decode verified deployment network parameters: %w", err)
	}
	if !parameters.Enabled.Value {
		return result, nil
	}
	if _, err := ParseID(parameters.VNet.Value, "Microsoft.Network/virtualNetworks"); err != nil {
		return result, err
	}
	subnet := parameters.VNet.Value + "/subnets/" + parameters.Subnet.Value
	if _, err := ParseID(subnet, "Microsoft.Network/virtualNetworks/subnets"); err != nil {
		return result, err
	}
	duration := authString(properties.Duration)
	if properties.Timestamp == nil || !strings.HasPrefix(duration, "PT") {
		return result, fmt.Errorf("network birth requires the verified deployment terminal timestamp and duration")
	}
	elapsed, err := time.ParseDuration(strings.NewReplacer("H", "h", "M", "m", "S", "s").Replace(duration[2:]))
	if err != nil || elapsed <= 0 {
		return result, fmt.Errorf("network birth requires a valid positive deployment duration")
	}
	result.end, result.start = *properties.Timestamp, properties.Timestamp.Add(-elapsed)
	result.network = FoundryNetworkState{
		VNetID: parameters.VNet.Value, PESubnetID: subnet,
		DNSResourceGroup: parameters.DNSGroup.Value, DNSSubscription: parameters.DNSSubscription.Value,
		Resources: map[string]FoundryNetworkResource{},
	}
	result.outputs = map[string]bool{}
	for _, resource := range properties.OutputResources {
		if resource != nil {
			result.outputs[strings.ToLower(authString(resource.ID))] = true
		}
	}
	return result, nil
}

func (l *ownedLifecycle) networkBirthWindows(
	ctx context.Context, plan Plan, state *PreparationState,
) ([]networkBirthWindow, error) {
	client, err := armresources.NewDeploymentsClient(plan.SubscriptionID, l.credential, l.options)
	if err != nil {
		return nil, err
	}
	var windows []networkBirthWindow
	for _, attempt := range state.Foundry.Attempts {
		id, err := subscriptionDeploymentID(attempt.ID, plan.SubscriptionID)
		if err != nil {
			return nil, err
		}
		found, err := client.GetAtSubscriptionScope(ctx, id.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("read exact verified deployment network birth interval: %w", err)
		}
		if found.Properties == nil {
			return nil, fmt.Errorf("verified deployment network properties are unavailable")
		}
		window, err := networkWindow(found.Properties)
		if err != nil {
			return nil, err
		}
		if window.network.VNetID != "" {
			windows = append(windows, window)
		}
	}
	return windows, nil
}

func (l *ownedLifecycle) expandedNetworkResources(
	ctx context.Context, plan Plan,
) (map[string]*armresources.GenericResourceExpanded, error) {
	client, err := armresources.NewClient(plan.SubscriptionID, l.credential, l.options)
	if err != nil {
		return nil, err
	}
	pager := client.NewListByResourceGroupPager(plan.ResourceGroup, &armresources.ClientListByResourceGroupOptions{
		Expand: new("createdTime,changedTime,provisioningState"),
	})
	result := map[string]*armresources.GenericResourceExpanded{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("read ARM resource birth inventory in the recorded resource group: %w", err)
		}
		for _, resource := range page.Value {
			if resource == nil || authString(resource.ID) == "" {
				return nil, fmt.Errorf("ARM resource birth inventory contains an incomplete entry")
			}
			key := strings.ToLower(authString(resource.ID))
			if _, duplicate := result[key]; duplicate {
				return nil, fmt.Errorf("ARM resource birth inventory contains duplicate IDs")
			}
			result[key] = resource
		}
	}
	return result, nil
}

func proveNetworkBirth(
	id string, actual *armresources.GenericResourceExpanded, windows []networkBirthWindow, parent string, inferred bool,
) error {
	if actual == nil || actual.CreatedTime == nil || actual.CreatedTime.IsZero() {
		return fmt.Errorf("network resource %s has no authoritative ARM createdTime; refusing ownership inference", id)
	}
	binding := id
	if parent != "" {
		binding = parent
	}
	bound := false
	for _, window := range windows {
		if !window.outputs[strings.ToLower(binding)] || (!inferred && !window.outputs[strings.ToLower(id)]) {
			continue
		}
		bound = true
		if !actual.CreatedTime.Before(window.start) && !actual.CreatedTime.After(window.end) {
			return nil
		}
	}
	if !bound {
		return fmt.Errorf("network resource %s lacks exact root output-resource binding", id)
	}
	return fmt.Errorf("network resource %s predates or lies outside its verified deployment creation window", id)
}

func (l *ownedLifecycle) inventoryFoundryNetwork(ctx context.Context, plan Plan, state *PreparationState) error {
	created := map[string]bool{}
	if _, err := l.deploymentGrantTargets(ctx, plan, state.Foundry, nil, created); err != nil {
		return err
	}
	targets := map[string]string{}
	for id := range created {
		parsed, err := arm.ParseResourceID(id)
		if err != nil || !validResourceIDSyntax(id) {
			return fmt.Errorf("network operation resource ID is invalid")
		}
		kind := parsed.ResourceType.String()
		if !networkCleanupType(kind) {
			continue
		}
		if !strings.EqualFold(parsed.SubscriptionID, plan.SubscriptionID) ||
			!strings.EqualFold(parsed.ResourceGroupName, plan.ResourceGroup) {
			return fmt.Errorf("network Create target lies outside the recorded target resource group")
		}
		for _, canonical := range []string{privateEndpointType, privateZoneGroupType, privateZoneType, privateLinkType} {
			if strings.EqualFold(kind, canonical) {
				targets[id] = canonical
			}
		}
	}
	windows, err := l.networkBirthWindows(ctx, plan, state)
	if err != nil {
		return err
	}
	if len(targets) == 0 && state.Foundry.Network == nil {
		if len(windows) == 0 {
			return nil
		}
		expanded, err := l.expandedNetworkResources(ctx, plan)
		if err != nil {
			return err
		}
		for id := range expanded {
			resource, err := arm.ParseResourceID(id)
			if err != nil || networkCleanupType(resource.ResourceType.String()) ||
				strings.EqualFold(resource.ResourceType.String(), privateNICType) {
				return fmt.Errorf("selected network resources lack exact verified Create evidence; deletion is blocked")
			}
		}
		return nil
	}
	if len(windows) == 0 {
		return fmt.Errorf("network Create targets lack verified selected network parameters")
	}
	topology := windows[0].network
	for _, window := range windows[1:] {
		n := window.network
		if !strings.EqualFold(n.VNetID, topology.VNetID) || !strings.EqualFold(n.PESubnetID, topology.PESubnetID) ||
			!strings.EqualFold(n.DNSResourceGroup, topology.DNSResourceGroup) ||
			!strings.EqualFold(n.DNSSubscription, topology.DNSSubscription) {
			return fmt.Errorf("recorded deployments contain conflicting network selections; refusing deletion")
		}
	}
	if state.Foundry.Network == nil {
		state.Foundry.Network = &topology
	}
	network := state.Foundry.Network
	if !strings.EqualFold(network.VNetID, topology.VNetID) || !strings.EqualFold(network.PESubnetID, topology.PESubnetID) ||
		!strings.EqualFold(network.DNSResourceGroup, topology.DNSResourceGroup) ||
		!strings.EqualFold(network.DNSSubscription, topology.DNSSubscription) {
		return fmt.Errorf("journaled network topology differs from the verified deployment parameters")
	}
	if network.Resources == nil {
		network.Resources = map[string]FoundryNetworkResource{}
	}
	expanded, err := l.expandedNetworkResources(ctx, plan)
	if err != nil {
		return err
	}
	for id, kind := range targets {
		if err := l.observeNetworkTarget(ctx, plan, state, id, kind, windows, expanded); err != nil {
			return err
		}
	}
	for id, record := range network.Resources {
		if record.Type != privateNICType && targets[id] == "" {
			return fmt.Errorf("journaled network resource lost its exact verified Create evidence")
		}
		if record.Type == privateNICType {
			if err := l.observeNetworkTarget(ctx, plan, state, id, record.Type, windows, expanded); err != nil {
				return err
			}
		}
	}
	if err := l.verifyNetworkTopology(ctx, plan, state); err != nil {
		return err
	}
	return l.journal.Save(ctx, *state)
}
