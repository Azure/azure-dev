// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// Input is resolved public input, with no customer identity fields.
type Input struct {
	ClusterID      string
	StorageID      string
	RegistryID     string
	AgentSubnetID  string
	SubscriptionID string
	ResourceGroup  string
	Location       string
	Environment    string
	ProjectPath    string
}

// ParseID validates the exact ARM type and complete subscription/RG hierarchy.
func ParseID(value, resourceType string) (*arm.ResourceID, error) {
	if !validResourceIDSyntax(value) {
		return nil, fmt.Errorf("expected a complete %s resource ID without URL credentials or extra components", resourceType)
	}
	id, err := arm.ParseResourceID(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s resource ID: %w", resourceType, err)
	}

	if id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
		!strings.EqualFold(id.ResourceType.String(), resourceType) ||
		strings.ContainsAny(value, "?#\\ \t\r\n") {
		return nil, fmt.Errorf("expected a complete %s resource ID", resourceType)
	}
	return id, nil
}

func validResourceIDSyntax(value string) bool {
	return strings.HasPrefix(strings.ToLower(value), "/subscriptions/") &&
		!strings.ContainsAny(value, "?#%\\@:\x00 \t\r\n") && !strings.Contains(value, "//")
}

// BuildPlan resolves creation/reuse once, without Azure reads or writes.
func BuildPlan(in Input) (Plan, error) {
	cluster, err := ParseID(in.ClusterID, "Microsoft.ContainerService/managedClusters")
	if err != nil {
		return Plan{}, fmt.Errorf("I01 clusterResourceId: %w", err)
	}
	if in.SubscriptionID == "" || in.ResourceGroup == "" || in.Location == "" || in.Environment == "" {
		return Plan{}, fmt.Errorf("I03: target subscription, resource group, location and environment are required")
	}
	parent := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", in.SubscriptionID, in.ResourceGroup)
	sum := sha256.Sum256([]byte(strings.ToLower(parent) + "|" + in.Environment + "|" + in.ProjectPath))
	token := fmt.Sprintf("%x", sum[:8])
	p := Plan{
		SubscriptionID: in.SubscriptionID, ResourceGroup: in.ResourceGroup,
		Location: in.Location, Environment: in.Environment, Owner: token,
		Cluster: Resource{ID: in.ClusterID, Parent: cluster.Parent.String(), Name: cluster.Name, Supplied: true},
		Storage: Resource{ID: parent + "/providers/Microsoft.Storage/storageAccounts/azd" + token,
			Parent: parent, Name: "azd" + token},
		Registry: Resource{ID: parent + "/providers/Microsoft.ContainerRegistry/registries/azd" + token,
			Parent: parent, Name: "azd" + token},
		Hosting: Resource{ID: parent + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/hosting-" + token,
			Parent: parent, Name: "hosting-" + token},
		Workload: Resource{ID: parent + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/workload-" + token,
			Parent: parent, Name: "workload-" + token},
	}
	for _, item := range []struct {
		value string
		kind  string
		out   *Resource
	}{
		{in.StorageID, "Microsoft.Storage/storageAccounts", &p.Storage},
		{in.RegistryID, "Microsoft.ContainerRegistry/registries", &p.Registry},
		{in.AgentSubnetID, "Microsoft.Network/virtualNetworks/subnets", &p.AgentSubnet},
	} {
		if item.value == "" {
			continue
		}
		id, err := ParseID(item.value, item.kind)
		if err != nil {
			return Plan{}, fmt.Errorf("I02: %w", err)
		}
		*item.out = Resource{ID: item.value, Parent: id.Parent.String(), Name: id.Name, Supplied: true}
	}
	// Target resources may not exist. Subscription is the conservative existing
	// ancestor until read-only resource-group discovery refines it.
	ancestor := "/subscriptions/" + in.SubscriptionID
	for _, r := range []*Resource{&p.Storage, &p.Registry, &p.Hosting, &p.Workload} {
		if !r.Supplied {
			r.Parent = ancestor
		}
	}
	add := func(checkID, action, scope string) {
		p.Operations = append(p.Operations, Operation{CheckID: checkID, Action: action, Scope: scope})
	}
	add("P01", "Microsoft.Resources/subscriptions/resourceGroups/write", ancestor)
	add("P08", "Microsoft.Resources/subscriptions/resourceGroups/read", ancestor)
	for _, action := range []string{"write", "read", "assign/action"} {
		add("P02", "Microsoft.ManagedIdentity/userAssignedIdentities/"+action, ancestor)
	}
	add("P03", "Microsoft.ContainerService/managedClusters/write", cluster.Parent.String())
	networkScope := ancestor
	if p.AgentSubnet.Supplied {
		subnet, _ := arm.ParseResourceID(p.AgentSubnet.ID)
		networkScope = fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subnet.SubscriptionID, subnet.ResourceGroupName)
	}
	add("P03", "Microsoft.Network/virtualNetworks/write", networkScope)
	if !p.Storage.Supplied {
		add("P04", "Microsoft.Storage/storageAccounts/write", p.Storage.Parent)
	}
	if !p.Registry.Supplied {
		add("P05", "Microsoft.ContainerRegistry/registries/write", p.Registry.Parent)
	}
	for _, action := range []string{"managedClusters/read", "managedClusters/agentPools/read"} {
		add("P08", "Microsoft.ContainerService/"+action, p.Cluster.ID)
	}
	add("P08", "Microsoft.Compute/skus/read", "/subscriptions/"+cluster.SubscriptionID)
	subscriptions := []string{strings.ToLower(in.SubscriptionID), strings.ToLower(cluster.SubscriptionID)}
	for _, r := range []struct {
		resource Resource
		action   string
	}{
		{p.Storage, "Microsoft.Storage/storageAccounts/read"},
		{p.Registry, "Microsoft.ContainerRegistry/registries/read"},
		{p.AgentSubnet, "Microsoft.Network/virtualNetworks/subnets/read"},
	} {
		if r.resource.Supplied {
			add("P08", r.action, r.resource.ID)
			id, _ := arm.ParseResourceID(r.resource.ID)
			subscriptions = append(subscriptions, strings.ToLower(id.SubscriptionID))
		} else if r.resource.Parent != "" {
			add("P08", r.action, r.resource.Parent)
		}
	}
	slices.Sort(subscriptions)
	for _, subscription := range slices.Compact(subscriptions) {
		add("P08", "Microsoft.Resources/subscriptions/read", "/subscriptions/"+subscription)
	}
	if p.AgentSubnet.Supplied {
		add("P09", "Microsoft.Network/virtualNetworks/subnets/join/action", p.AgentSubnet.ID)
	}
	for _, action := range []string{
		"Microsoft.CognitiveServices/accounts/write", "Microsoft.CognitiveServices/accounts/projects/write",
		"Microsoft.Resources/deployments/write", "Microsoft.Resources/deployments/read",
		"Microsoft.Resources/deployments/operations/read",
	} {
		add("P09", action, ancestor)
	}
	return p, nil
}
