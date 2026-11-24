// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore armcompute

package onboarding

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/v4"
)

// Managed-VNet pools may omit vnetSubnetID. Resolve their subnet through the
// cluster-owned node RG and the selected pool's actual VMSS network configuration,
// never through generated VNet/VMSS names or unrelated pools.
func (c *sdkResourceChecker) nodeSubnetIDs(
	ctx context.Context, plan Plan, cluster *armcontainerservice.ManagedCluster, pool *armcontainerservice.AgentPool,
) ([]string, Report) {
	if pool != nil && pool.Properties != nil && resourceValue(pool.Properties.VnetSubnetID) != "" {
		return []string{resourceValue(pool.Properties.VnetSubnetID)}, nil
	}
	check := resourceCheck("agent-subnet", plan.Cluster.ID, networkResourceType,
		"The selected AKS node pool's node subnet could not be verified")
	clusterID, err := existingResourceID(plan.Cluster.ID, "Microsoft.ContainerService/managedClusters")
	if err != nil || cluster == nil || cluster.Properties == nil ||
		!strings.EqualFold(resourceValue(cluster.ID), plan.Cluster.ID) || pool == nil || pool.Properties == nil {
		check.FailureReasons = []string{"Selected cluster or node pool metadata is unavailable or mismatched"}
		return nil, Report{check}
	}
	poolName := resourceValue(pool.Name)
	nodeRG := resourceValue(cluster.Properties.NodeResourceGroup)
	if poolName == "" || strings.ContainsAny(poolName, "/\\?#%{} \t\r\n") ||
		!strings.EqualFold(resourceValue(pool.ID), plan.Cluster.ID+"/agentPools/"+poolName) ||
		nodeRG == "" || strings.ContainsAny(nodeRG, "/\\?#%{} \t\r\n") {
		check.FailureReasons = []string{"Selected node pool identity or cluster node resource group could not be verified"}
		return nil, Report{check}
	}
	scope := "/subscriptions/" + clusterID.SubscriptionID + "/resourceGroups/" + nodeRG
	groups, err := armresources.NewResourceGroupsClient(clusterID.SubscriptionID, c.credential, c.options)
	if err != nil {
		check.Cause = err
		check.ShowCause = true
		return nil, Report{check}
	}
	group, err := groups.Get(ctx, nodeRG, nil)
	if err != nil {
		return nil, Report{managedNodeReadFailure(scope, "AKS node resource group", err)}
	}
	check.Scope = scope
	if !strings.EqualFold(resourceValue(group.ID), scope) ||
		!strings.EqualFold(resourceValue(group.ManagedBy), plan.Cluster.ID) {
		check.FailureReasons = []string{"Node resource group ownership by the selected AKS cluster could not be verified"}
		return nil, Report{check}
	}
	sets, err := armcompute.NewVirtualMachineScaleSetsClient(clusterID.SubscriptionID, c.credential, c.options)
	if err != nil {
		check.Cause = err
		check.ShowCause = true
		return nil, Report{check}
	}
	var subnetIDs []string
	matched := false
	pager := sets.NewListPager(nodeRG, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, Report{managedNodeReadFailure(scope, "AKS node virtual machine scale set configuration", err)}
		}
		for _, set := range page.Value {
			if set == nil || !strings.EqualFold(resourceValue(set.Tags["aks-managed-poolName"]), poolName) {
				continue
			}
			ids, err := managedPoolSubnetIDs(set, scope)
			if err != nil {
				check.FailureReasons = []string{
					"Selected node pool's virtual machine scale set subnet configuration is incomplete",
				}
				check.Cause = err
				check.ShowCause = true
				return nil, Report{check}
			}
			matched = true
			subnetIDs = append(subnetIDs, ids...)
		}
	}
	if !matched {
		check.FailureReasons = []string{"No virtual machine scale set could be associated with the selected AKS node pool"}
		return nil, Report{check}
	}
	slices.Sort(subnetIDs)
	subnetIDs = slices.Compact(subnetIDs)
	check = resourceResult(check, Pass)
	return subnetIDs, Report{check}
}

func managedNodeReadFailure(scope, operation string, err error) Check {
	check := readFailure("agent-subnet", scope, networkResourceType, operation, err)
	check.Status = Unverified
	check.ShowCause = true
	return check
}

func managedPoolSubnetIDs(set *armcompute.VirtualMachineScaleSet, groupID string) ([]string, error) {
	id, err := existingResourceID(resourceValue(set.ID), "Microsoft.Compute/virtualMachineScaleSets")
	if err != nil || !strings.EqualFold(id.Parent.String(), groupID) {
		return nil, fmt.Errorf("selected pool VMSS resource ID does not belong to the cluster-owned node resource group")
	}
	p := set.Properties
	if p == nil || resourceValue(p.ProvisioningState) != "Succeeded" ||
		p.VirtualMachineProfile == nil || p.VirtualMachineProfile.NetworkProfile == nil {
		return nil, fmt.Errorf("selected pool VMSS lacks a succeeded provisioning state or network profile")
	}
	interfaces := p.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations
	if len(interfaces) == 0 {
		return nil, fmt.Errorf("selected pool VMSS has no network interface configurations")
	}
	var ids []string
	for _, nic := range interfaces {
		if nic == nil || nic.Properties == nil || len(nic.Properties.IPConfigurations) == 0 {
			return nil, fmt.Errorf("selected pool VMSS has incomplete network interface configuration")
		}
		for _, ip := range nic.Properties.IPConfigurations {
			if ip == nil || ip.Properties == nil || ip.Properties.Subnet == nil {
				return nil, fmt.Errorf("selected pool VMSS IP configuration has no subnet reference")
			}
			raw := resourceValue(ip.Properties.Subnet.ID)
			if _, err := existingResourceID(raw, "Microsoft.Network/virtualNetworks/subnets"); err != nil {
				return nil, fmt.Errorf("selected pool VMSS subnet reference: %w", err)
			}
			ids = append(ids, raw)
		}
	}
	return ids, nil
}
