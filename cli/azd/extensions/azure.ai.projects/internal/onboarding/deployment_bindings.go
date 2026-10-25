// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

// DeploymentData binds the read-only plan to one ARM graph. Identity principals are ARM outputs, never inputs.
type DeploymentData struct {
	ClusterID                         string
	TenantID                          string
	ResourceGroupSupplied             bool
	Hosting                           Resource
	Workload                          Resource
	StorageID                         string
	StorageSupplied                   bool // ARM reference selection; existing journal ownership remains authoritative.
	RegistryID                        string
	RegistrySupplied                  bool
	RegistryABAC                      bool
	KubeletObjectID                   string
	StorageSKU                        string // Creation-only; reference branches never update the existing SKU.
	FederatedIdentityRoleDefinitionID string
	KubeletRoleAssignmentID           string // ARM reference only; existing journal ownership remains authoritative.
}

// StorageSKU selects Standard_ZRS except in the public regions without StorageV2 ZRS support.
func StorageSKU(location string) string {
	// Checked against Microsoft.Storage/skus (2023-05-01) on 2026-10-21, not zone counts.
	unsupported := []string{
		"australiacentral", "australiacentral2", "australiasoutheast", "brazilsoutheast", "canadaeast",
		"francesouth", "germanynorth", "koreasouth", "northcentralus", "norwaywest", "southafricawest",
		"southindia", "swedensouth", "switzerlandwest", "taiwannorth", "taiwannorthwest", "uaecentral",
		"ukwest", "westcentralus", "westindia", "westus",
	}
	if slices.Contains(unsupported, strings.ToLower(strings.TrimSpace(location))) {
		return "Standard_LRS"
	}
	return "Standard_ZRS"
}

// PlannedDeployment validates internally planned resource IDs without requiring future principal IDs.
func PlannedDeployment(plan Plan, facts Facts) (DeploymentData, error) {
	if plan.Network != nil {
		return DeploymentData{}, fmt.Errorf(
			"automatic hosting-subnet preparation is unsupported; retain old journals for cleanup")
	}
	if plan.Owner == "" || plan.Location == "" || facts.TenantID == "" || facts.KubeletObjectID == "" {
		return DeploymentData{}, fmt.Errorf(
			"deployment requires the planned owner/location and verified tenant/kubelet identity")
	}
	if plan.Hosting.Supplied || plan.Workload.Supplied || strings.EqualFold(plan.Hosting.ID, plan.Workload.ID) {
		return DeploymentData{}, fmt.Errorf("R04/R05: two distinct internally planned identities are required")
	}
	for _, item := range []struct {
		resource Resource
		kind     string
	}{
		{plan.Hosting, "Microsoft.ManagedIdentity/userAssignedIdentities"},
		{plan.Workload, "Microsoft.ManagedIdentity/userAssignedIdentities"},
		{plan.Storage, "Microsoft.Storage/storageAccounts"},
		{plan.Registry, "Microsoft.ContainerRegistry/registries"},
	} {
		id, err := ParseID(item.resource.ID, item.kind)
		if err != nil {
			return DeploymentData{}, err
		}
		if item.resource.Name != id.Name || !item.resource.Supplied &&
			(!strings.EqualFold(id.SubscriptionID, plan.SubscriptionID) ||
				!strings.EqualFold(id.ResourceGroupName, plan.ResourceGroup)) {
			return DeploymentData{}, fmt.Errorf("owned deployment resource differs from the planned target")
		}
	}
	return DeploymentData{
		ClusterID:             plan.Cluster.ID,
		TenantID:              facts.TenantID,
		ResourceGroupSupplied: plan.ResourceGroupSupplied, Hosting: plan.Hosting, Workload: plan.Workload,
		StorageID: plan.Storage.ID, StorageSupplied: plan.Storage.Supplied,
		RegistryID: plan.Registry.ID, RegistrySupplied: plan.Registry.Supplied, RegistryABAC: facts.RegistryABAC,
		KubeletObjectID: facts.KubeletObjectID, StorageSKU: StorageSKU(plan.Location),
	}, nil
}

// ResolveDeploymentBindings reads the G02 role and compatible existing G04 grant without any ARM mutations.
func ResolveDeploymentBindings(
	ctx context.Context, credential azcore.TokenCredential, options *arm.ClientOptions, plan Plan, facts Facts,
) (DeploymentData, error) {
	data, err := PlannedDeployment(plan, facts)
	if err != nil {
		return data, err
	}
	options = onboardingARMOptions(options)
	if !plan.Storage.Supplied {
		id, _ := ParseID(plan.Storage.ID, "Microsoft.Storage/storageAccounts")
		client, err := armstorage.NewAccountsClient(id.SubscriptionID, credential, options)
		if err != nil {
			return data, err
		}
		found, err := client.GetProperties(ctx, id.ResourceGroupName, id.Name, nil)
		if err == nil {
			if !strings.EqualFold(authString(found.ID), plan.Storage.ID) ||
				authString(found.Tags[preparationOwnerTag]) != plan.Owner {
				return data, fmt.Errorf(
					"R02: existing planned Storage is not the exact owner-tagged resource; refusing adoption")
			}
			// Lifecycle validation also requires the active R02 intent; an owner tag alone is not ownership.
			data.StorageSupplied = true
		} else if !responseNotFound(err) {
			return data, fmt.Errorf("R02: resolve existing owned Storage reference: %w", err)
		}
	}
	for _, grant := range grantRequests(plan, facts, Identity{}, Identity{}) {
		if grant.checkID == "G02" {
			data.FederatedIdentityRoleDefinitionID, err = resolveGrantRole(
				ctx, credential, options, plan.SubscriptionID, grant)
			if err != nil {
				return data, fmt.Errorf("resolve deployment G02 role: %w", err)
			}
		}
		if grant.checkID != "G04" || !plan.Registry.Supplied {
			continue
		}
		data.KubeletRoleAssignmentID, err = existingKubeletPullGrant(ctx, credential, options, plan, facts)
		if err != nil {
			return data, fmt.Errorf("resolve existing kubelet registry pull grant: %w", err)
		}
	}
	return data, nil
}

func compatibleGrant(assignment armauthorization.RoleAssignment, grant grantRequest) bool {
	p := assignment.Properties
	return p != nil && strings.EqualFold(authString(p.Scope), grant.scope) &&
		strings.EqualFold(authString(p.PrincipalID), grant.principalID) &&
		strings.EqualFold(authString(p.RoleDefinitionID), grant.roleID) &&
		p.PrincipalType != nil && *p.PrincipalType == armauthorization.PrincipalTypeServicePrincipal &&
		authString(p.Condition) == "" && authString(p.ConditionVersion) == "" &&
		authString(p.DelegatedManagedIdentityResourceID) == ""
}
