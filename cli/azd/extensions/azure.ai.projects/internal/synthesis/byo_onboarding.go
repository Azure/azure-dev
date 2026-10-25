// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// BYOOnboardingParameters is the private, read-only plan consumed by the deployment graph.
// Names and resource IDs come from onboarding.BuildPlan; principal IDs are resolved by ARM.
type BYOOnboardingParameters struct {
	Owner                             string `json:"owner"`
	AccountName                       string `json:"accountName"`
	ResourceGroupSupplied             bool   `json:"resourceGroupSupplied"`
	ClusterResourceID                 string `json:"clusterResourceId"`
	StorageAccountResourceID          string `json:"storageAccountResourceId"`
	StorageName                       string `json:"storageName"`
	StorageSupplied                   bool   `json:"storageSupplied"`
	StorageSKU                        string `json:"storageSku"`
	HostingIdentityName               string `json:"hostingIdentityName"`
	WorkloadIdentityName              string `json:"workloadIdentityName"`
	RegistryResourceID                string `json:"registryResourceId"`
	RegistryName                      string `json:"registryName"`
	RegistrySupplied                  bool   `json:"registrySupplied"`
	RegistryABAC                      bool   `json:"registryABAC"`
	KubeletObjectID                   string `json:"kubeletObjectId"`
	FederatedIdentityRoleDefinitionID string `json:"federatedIdentityRoleDefinitionId"`
	KubeletRoleAssignmentID           string `json:"kubeletRoleAssignmentId"`
}

// ApplyBYOOnboarding atomically binds an internally resolved plan without reading or creating resources.
func ApplyBYOOnboarding(result *Result, plan BYOOnboardingParameters) error {
	if result == nil || result.Hosting == nil {
		return fmt.Errorf("BYO onboarding requires a BYO-AKS synthesis result")
	}
	for name, value := range map[string]string{
		"owner": plan.Owner, "accountName": plan.AccountName,
		"hostingIdentityName": plan.HostingIdentityName, "workloadIdentityName": plan.WorkloadIdentityName,
		"kubeletObjectId": plan.KubeletObjectID, "federatedIdentityRoleDefinitionId": plan.FederatedIdentityRoleDefinitionID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("BYO onboarding %s is required", name)
		}
	}
	if strings.EqualFold(plan.HostingIdentityName, plan.WorkloadIdentityName) {
		return fmt.Errorf("BYO onboarding hosting and workload identities must be distinct")
	}
	for _, resource := range []struct {
		value, kind, name string
	}{
		{plan.ClusterResourceID, `Microsoft\.ContainerService/managedClusters`, ""},
		{plan.StorageAccountResourceID, `Microsoft\.Storage/storageAccounts`, plan.StorageName},
		{plan.RegistryResourceID, `Microsoft\.ContainerRegistry/registries`, plan.RegistryName},
	} {
		pattern := `(?i)^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/` + resource.kind + `/[^/]+$`
		if !regexp.MustCompile(pattern).MatchString(resource.value) ||
			strings.ContainsAny(resource.value, "?#%\\@:\x00 \t\r\n") {
			return fmt.Errorf("BYO onboarding requires a complete %s resource ID", resource.kind)
		}
		resourceName := resource.value[strings.LastIndex(resource.value, "/")+1:]
		if resource.name != "" && !strings.EqualFold(resource.name, resourceName) {
			return fmt.Errorf("BYO onboarding name must match its resource ID")
		}
	}
	if plan.StorageName == "" || plan.RegistryName == "" {
		return fmt.Errorf("BYO onboarding storageName and registryName are required")
	}
	if !strings.EqualFold(plan.ClusterResourceID, result.Hosting.ClusterResourceID) {
		return fmt.Errorf("BYO onboarding must preserve the explicitly supplied cluster")
	}
	if suppliedStorage := result.Hosting.StorageAccountResourceID; suppliedStorage != "" &&
		(!plan.StorageSupplied || !strings.EqualFold(plan.StorageAccountResourceID, suppliedStorage)) {
		return fmt.Errorf("BYO onboarding must preserve the explicitly supplied storage account")
	}
	if plan.StorageSKU != "Standard_ZRS" && plan.StorageSKU != "Standard_LRS" {
		return fmt.Errorf("BYO onboarding storageSku must be Standard_ZRS or Standard_LRS")
	}
	if !regexp.MustCompile(`(?i)^/subscriptions/[^/]+/providers/Microsoft\.Authorization/roleDefinitions/[^/]+$`).
		MatchString(plan.FederatedIdentityRoleDefinitionID) {
		return fmt.Errorf("BYO onboarding requires a complete federated identity role definition ID")
	}
	if plan.KubeletRoleAssignmentID != "" {
		assignment, err := arm.ParseResourceID(plan.KubeletRoleAssignmentID)
		if err != nil || assignment.Name == "" ||
			strings.ContainsAny(plan.KubeletRoleAssignmentID, "?#%\\@:\x00 \t\r\n") ||
			strings.Contains(plan.KubeletRoleAssignmentID, "//") ||
			!strings.EqualFold(assignment.ResourceType.String(), "Microsoft.Authorization/roleAssignments") {
			return fmt.Errorf("BYO onboarding existing kubelet role assignment ID is invalid")
		}
		registry, _ := arm.ParseResourceID(plan.RegistryResourceID)
		scope := assignment.Parent.String()
		if !strings.EqualFold(scope, plan.RegistryResourceID) &&
			!strings.EqualFold(scope, registry.Parent.String()) &&
			!strings.EqualFold(scope, "/subscriptions/"+registry.SubscriptionID) {
			return fmt.Errorf("BYO onboarding existing kubelet role assignment must apply at registry, RG or subscription")
		}
	}
	result.Parameters["byoOnboarding"] = plan
	result.Parameters["includeAcr"] = true
	return nil
}
