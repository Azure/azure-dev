// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"go.yaml.in/yaml/v3"
)

// AgentHostingInput selects the existing cluster used to host Foundry agents.
type AgentHostingInput struct {
	ClusterResourceID string `yaml:"clusterResourceId"`
}

type agentHostingParameter struct {
	Enabled                             bool   `json:"enabled"`
	HostingType                         string `json:"hostingType"`
	Name                                string `json:"name"`
	ClusterResourceID                   string `json:"clusterResourceId"`
	HostingManagementIdentityResourceID string `json:"hostingManagementIdentityResourceId"`
	StorageAccountResourceID            string `json:"storageAccountResourceId"`
	WorkloadIdentityResourceID          string `json:"workloadIdentityResourceId"`
}

func validateAgentHostingInputs(node yaml.Node, service string) error {
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return err
	}
	hosting, exists := fields["agentHosting"]
	if !exists {
		return nil
	}
	if hosting.Kind != yaml.MappingNode {
		return fmt.Errorf("services.%s.agentHosting: expected an object containing clusterResourceId", service)
	}
	for i := 0; i < len(hosting.Content); i += 2 {
		name := hosting.Content[i].Value
		if name != "clusterResourceId" {
			return fmt.Errorf("services.%s.agentHosting.%s: unsupported property; "+
				"azd creates the storage account and both managed identities", service, name)
		}
		value := hosting.Content[i+1]
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return fmt.Errorf("services.%s.agentHosting.clusterResourceId: expected a nonempty resource ID", service)
		}
	}
	return nil
}

func synthesizeAgentHosting(
	input *AgentHostingInput, service string, env map[string]string, resolve bool,
) (*AgentHostingInput, error) {
	if input == nil {
		return nil, nil
	}
	field := fmt.Sprintf("services.%s.agentHosting.clusterResourceId", service)
	value := strings.TrimSpace(input.ClusterResourceID)
	if err := ValidateEnvReferences(value); err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	if resolve {
		resolved, err := resolveVars(value, env)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		value = strings.TrimSpace(resolved)
	}
	if value == "" {
		return nil, fmt.Errorf("%s: required", field)
	}
	if resolve || !containsVarRef(value) {
		id, err := arm.ParseResourceID(value)
		if err != nil || id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
			!strings.EqualFold(id.ResourceType.String(), "Microsoft.ContainerService/managedClusters") ||
			len(strings.Split(value, "/")) != 9 || strings.ContainsAny(value, "?#%\\@:\x00 \t\r\n") {
			return nil, fmt.Errorf("%s: expected a complete Microsoft.ContainerService/managedClusters resource ID", field)
		}
	}
	return &AgentHostingInput{ClusterResourceID: value}, nil
}

// BYOOnboardingParameters binds automatically created prerequisites to the standard deployment graph.
type BYOOnboardingParameters struct {
	DeploymentID                      string `json:"deploymentId"`
	ResourceGroupSupplied             bool   `json:"resourceGroupSupplied"`
	StorageName                       string `json:"storageName"`
	StorageSKU                        string `json:"storageSku"`
	StorageAccountResourceID          string `json:"storageAccountResourceId"`
	HostingIdentityName               string `json:"hostingIdentityName"`
	HostingIdentityResourceID         string `json:"hostingIdentityResourceId"`
	WorkloadIdentityName              string `json:"workloadIdentityName"`
	WorkloadIdentityResourceID        string `json:"workloadIdentityResourceId"`
	KubeletObjectID                   string `json:"kubeletObjectId"`
	FederatedIdentityRoleDefinitionID string `json:"federatedIdentityRoleDefinitionId"`
}

// ApplyBYOOnboarding binds a validated default-resource plan without performing I/O.
func ApplyBYOOnboarding(result *Result, plan BYOOnboardingParameters) error {
	if result == nil || result.Hosting == nil || plan.DeploymentID == "" || plan.KubeletObjectID == "" ||
		plan.FederatedIdentityRoleDefinitionID == "" || plan.StorageName == "" ||
		plan.HostingIdentityName == "" || plan.WorkloadIdentityName == "" ||
		strings.EqualFold(plan.HostingIdentityResourceID, plan.WorkloadIdentityResourceID) {
		return fmt.Errorf("BYO AKS requires a complete plan with separate hosting and workload identities")
	}
	for _, resource := range []struct{ id, kind, name string }{
		{plan.StorageAccountResourceID, "Microsoft.Storage/storageAccounts", plan.StorageName},
		{plan.HostingIdentityResourceID, "Microsoft.ManagedIdentity/userAssignedIdentities", plan.HostingIdentityName},
		{plan.WorkloadIdentityResourceID, "Microsoft.ManagedIdentity/userAssignedIdentities", plan.WorkloadIdentityName},
	} {
		id, err := arm.ParseResourceID(resource.id)
		if err != nil || id.SubscriptionID == "" || id.ResourceGroupName == "" ||
			id.Name != resource.name || !strings.EqualFold(id.ResourceType.String(), resource.kind) ||
			strings.ContainsAny(resource.id, "?#%\\@:\x00 \t\r\n") {
			return fmt.Errorf("BYO AKS plan requires complete, matching resource IDs")
		}
	}
	if plan.StorageSKU != "Standard_ZRS" && plan.StorageSKU != "Standard_LRS" {
		return fmt.Errorf("BYO AKS storage SKU must be Standard_ZRS or Standard_LRS")
	}
	result.Parameters["byoOnboarding"] = plan
	result.Parameters["includeAcr"] = true
	return nil
}
