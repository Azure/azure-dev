// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// Input is resolved public input, with no customer identity fields.
type Input struct {
	ClusterID      string
	StorageID      string
	RegistryID     string
	AgentSubnetID  string // Resolved standard network.agentSubnet, never an agentHosting property.
	SubscriptionID string
	ResourceGroup  string
	Location       string
	Environment    string
	ProjectPath    string
	FoundryName    string
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
	var part strings.Builder
	for _, ch := range strings.ToLower(in.FoundryName) {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
			part.WriteRune(ch)
			if part.Len() == 6 {
				break
			}
		}
	}
	if part.Len() == 0 {
		return Plan{}, fmt.Errorf("I03: effective foundryProjectName must contain an ASCII letter or digit for resource naming")
	}
	parent := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", in.SubscriptionID, in.ResourceGroup)
	sum := sha256.Sum256([]byte(strings.ToLower(parent) + "|" + in.Environment + "|" + in.ProjectPath))
	token := fmt.Sprintf("%x", sum[:8])
	storageName, hostingName, workloadName := "sa"+part.String()+token,
		"hosting-"+part.String()+token, "workload-"+part.String()+token
	p := Plan{
		SubscriptionID: in.SubscriptionID, ResourceGroup: in.ResourceGroup,
		Location: in.Location, Environment: in.Environment, Owner: token,
		RegistryCustomerSupplied: in.RegistryID != "",
		Cluster:                  Resource{ID: in.ClusterID, Parent: cluster.Parent.String(), Name: cluster.Name, Supplied: true},
		Storage: Resource{ID: parent + "/providers/Microsoft.Storage/storageAccounts/" + storageName,
			Parent: parent, Name: storageName},
		Registry: Resource{ID: parent + "/providers/Microsoft.ContainerRegistry/registries/azd" + token,
			Parent: parent, Name: "azd" + token},
		Hosting: Resource{ID: parent + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + hostingName,
			Parent: parent, Name: hostingName},
		Workload: Resource{ID: parent + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + workloadName,
			Parent: parent, Name: workloadName},
	}
	for _, item := range []struct {
		value string
		kind  string
		out   *Resource
	}{
		{in.StorageID, "Microsoft.Storage/storageAccounts", &p.Storage},
		{in.RegistryID, "Microsoft.ContainerRegistry/registries", &p.Registry},
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
	if in.AgentSubnetID != "" {
		id, err := ParseID(in.AgentSubnetID, "Microsoft.Network/virtualNetworks/subnets")
		if err != nil {
			return Plan{}, fmt.Errorf("N02: %w", err)
		}
		p.AgentSubnet = Resource{ID: in.AgentSubnetID, Parent: id.Parent.String(), Name: id.Name, Supplied: true}
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
	return p, nil
}
