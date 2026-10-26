// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
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
	check := resourceCheck("N02", plan.Cluster.ID,
		"selected AKS pool Node subnet from cluster-owned node resource group and ARM VMSS network configuration")
	check.Missing = "cluster nodeResourceGroup, exact managedBy cluster binding, selected pool VMSS and subnet IDs"
	check.Remediation = "allow resourceGroups/read and virtualMachineScaleSets/read on the AKS node resource group, " +
		"and virtualNetworks/subnets/read on its node subnet; retain fail-closed validation if evidence is unavailable"
	clusterID, err := existingResourceID(plan.Cluster.ID, "Microsoft.ContainerService/managedClusters")
	if err != nil || cluster == nil || cluster.Properties == nil ||
		!strings.EqualFold(resourceValue(cluster.ID), plan.Cluster.ID) || pool == nil || pool.Properties == nil {
		check.Observed = "selected cluster or pool metadata is unavailable or mismatched"
		return nil, Report{check}
	}
	poolName := resourceValue(pool.Name)
	nodeRG := resourceValue(cluster.Properties.NodeResourceGroup)
	if poolName == "" || strings.ContainsAny(poolName, "/\\?#%{} \t\r\n") ||
		!strings.EqualFold(resourceValue(pool.ID), plan.Cluster.ID+"/agentPools/"+poolName) ||
		nodeRG == "" || strings.ContainsAny(nodeRG, "/\\?#%{} \t\r\n") {
		check.Observed = "selected pool resource ID or cluster nodeResourceGroup is absent or invalid"
		return nil, Report{check}
	}
	scope := "/subscriptions/" + clusterID.SubscriptionID + "/resourceGroups/" + nodeRG
	groups, err := armresources.NewResourceGroupsClient(clusterID.SubscriptionID, c.credential, c.options)
	if err != nil {
		check.Cause = err
		return nil, Report{check}
	}
	group, err := groups.Get(ctx, nodeRG, nil)
	if err != nil {
		return nil, Report{managedNodeReadFailure(scope, "Microsoft.Resources/subscriptions/resourceGroups/read", err)}
	}
	check.Scope = scope
	if !strings.EqualFold(resourceValue(group.ID), scope) ||
		!strings.EqualFold(resourceValue(group.ManagedBy), plan.Cluster.ID) {
		check.Observed = "node resource group ID/managedBy does not prove association with the selected AKS cluster"
		return nil, Report{check}
	}
	sets, err := armcompute.NewVirtualMachineScaleSetsClient(clusterID.SubscriptionID, c.credential, c.options)
	if err != nil {
		check.Cause = err
		return nil, Report{check}
	}
	var subnetIDs []string
	matches := 0
	pager := sets.NewListPager(nodeRG, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, Report{managedNodeReadFailure(scope, "Microsoft.Compute/virtualMachineScaleSets/read", err)}
		}
		for _, set := range page.Value {
			if set == nil || !strings.EqualFold(resourceValue(set.Tags["aks-managed-poolName"]), poolName) {
				continue
			}
			ids, err := managedPoolSubnetIDs(set, scope)
			if err != nil {
				check.Observed = "selected pool VMSS subnet evidence is incomplete or inconsistent"
				check.Cause = err
				return nil, Report{check}
			}
			matches++
			subnetIDs = append(subnetIDs, ids...)
		}
	}
	if matches == 0 {
		check.Observed = "no VMSS in the cluster-owned node resource group identifies the selected pool"
		return nil, Report{check}
	}
	slices.Sort(subnetIDs)
	subnetIDs = slices.Compact(subnetIDs)
	check = resourceResult(check, Pass, fmt.Sprintf(
		"node resource group managedBy matches selected cluster; %d selected-pool VMSS network profiles; subnet IDs=%s",
		matches, strings.Join(subnetIDs, ", ")))
	return subnetIDs, Report{check}
}

func managedNodeReadFailure(scope, action string, err error) Check {
	check := readFailure("N02", scope, action, err)
	check.Status = Unverified
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
