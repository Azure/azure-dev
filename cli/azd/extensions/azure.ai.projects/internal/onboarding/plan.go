// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// Input combines public selections with the unchanged template's evaluated default resource IDs.
type Input struct {
	SubscriptionID     string
	ResourceGroup      string
	GroupExists        bool
	ClusterID          string
	StorageID          string
	HostingIdentityID  string
	WorkloadIdentityID string
	DefaultStorageID   string
	DefaultHostingID   string
	DefaultWorkloadID  string
}

// ParseID validates the exact ARM type and complete subscription/RG hierarchy.
func ParseID(value, resourceType string) (*arm.ResourceID, error) {
	if !validResourceIDSyntax(value) {
		return nil, fmt.Errorf(
			"expected a complete %s resource ID without URL credentials or extra components", resourceType)
	}
	id, err := arm.ParseResourceID(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s resource ID: %w", resourceType, err)
	}
	segments := 9
	if strings.EqualFold(resourceType, "Microsoft.Network/virtualNetworks/subnets") {
		segments = 11
	}
	if id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
		!strings.EqualFold(id.ResourceType.String(), resourceType) || len(strings.Split(value, "/")) != segments {
		return nil, fmt.Errorf("expected a complete %s resource ID", resourceType)
	}
	return id, nil
}

func validResourceIDSyntax(value string) bool {
	return strings.HasPrefix(strings.ToLower(value), "/subscriptions/") &&
		!strings.ContainsAny(value, "?#%\\@:\x00 \t\r\n") && !strings.Contains(value, "//")
}

// BuildPlan independently selects each reference or default without Azure reads or writes.
func BuildPlan(in Input) (Plan, error) {
	cluster, err := ParseID(in.ClusterID, "Microsoft.ContainerService/managedClusters")
	if err != nil {
		return Plan{}, fmt.Errorf("aks-id clusterResourceId: %w", err)
	}
	parent := "/subscriptions/" + in.SubscriptionID + "/resourceGroups/" + in.ResourceGroup
	if _, err := arm.ParseResourceID(parent); err != nil || in.SubscriptionID == "" || in.ResourceGroup == "" ||
		strings.ContainsAny(in.SubscriptionID+in.ResourceGroup, "/\\?#%@:\x00 \t\r\n") {
		return Plan{}, fmt.Errorf("resource-plan: target subscription and resource group are required")
	}
	ancestor := "/subscriptions/" + in.SubscriptionID
	if in.GroupExists {
		ancestor = parent
	}
	plan := Plan{
		SubscriptionID: in.SubscriptionID,
		Cluster:        Resource{ID: in.ClusterID, Parent: cluster.Parent.String(), Supplied: true},
	}
	for _, item := range []struct {
		supplied, fallback, kind string
		out                      *Resource
	}{
		{in.StorageID, in.DefaultStorageID, "Microsoft.Storage/storageAccounts", &plan.Storage},
		{in.HostingIdentityID, in.DefaultHostingID, "Microsoft.ManagedIdentity/userAssignedIdentities", &plan.Hosting},
		{in.WorkloadIdentityID, in.DefaultWorkloadID, "Microsoft.ManagedIdentity/userAssignedIdentities", &plan.Workload},
	} {
		value := item.supplied
		if value == "" {
			value = item.fallback
		}
		id, err := ParseID(value, item.kind)
		if err != nil {
			return Plan{}, fmt.Errorf("resource-refs: %w", err)
		}
		*item.out = Resource{ID: value, Parent: id.Parent.String(), Supplied: item.supplied != ""}
		if !item.out.Supplied {
			if !strings.EqualFold(id.Parent.String(), parent) {
				return Plan{}, fmt.Errorf("resource-plan: default supporting resource differs from the deployment target")
			}
			item.out.Parent = ancestor
		}
	}
	return plan, nil
}
