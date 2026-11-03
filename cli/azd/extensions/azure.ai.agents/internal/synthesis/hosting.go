// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS

package synthesis

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"go.yaml.in/yaml/v3"
)

// BYOAKSFeatureID identifies the existing-AKS hosting preview.
const BYOAKSFeatureID = "agents.byo_aks"

// BYOAKSEnvVar is the process-environment switch for Agents project initialization and adoption.
const BYOAKSEnvVar = "AZD_ALPHA_ENABLE_AGENTS_BYO_AKS"

// AgentHostingInput selects the existing cluster and optional supporting resources.
type AgentHostingInput struct {
	ClusterResourceID                   string `yaml:"clusterResourceId"`
	StorageAccountResourceID            string `yaml:"storageAccountResourceId"`
	HostingManagementIdentityResourceID string `yaml:"hostingManagementIdentityResourceId"`
	WorkloadIdentityResourceID          string `yaml:"workloadIdentityResourceId"`
}

type agentHostingParameter struct {
	Enabled                             bool   `json:"enabled"`
	HostingType                         string `json:"hostingType"`
	Name                                string `json:"name"`
	ClusterResourceID                   string `json:"clusterResourceId"`
	StorageAccountResourceID            string `json:"storageAccountResourceId"`
	HostingManagementIdentityResourceID string `json:"hostingManagementIdentityResourceId"`
	WorkloadIdentityResourceID          string `json:"workloadIdentityResourceId"`
}

// ValidateAgentHostingInputs checks hosting selections without synthesizing unrelated service inputs.
func ValidateAgentHostingInputs(node yaml.Node, service string) error {
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
		switch name {
		case "clusterResourceId", "storageAccountResourceId",
			"hostingManagementIdentityResourceId", "workloadIdentityResourceId":
		default:
			return fmt.Errorf("services.%s.agentHosting.%s: unsupported property", service, name)
		}
		value := hosting.Content[i+1]
		if name != "clusterResourceId" &&
			(value.Tag == "!!null" || value.Tag == "!!str" && value.Value == "") {
			continue
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || strings.TrimSpace(value.Value) == "" {
			return fmt.Errorf("services.%s.agentHosting.%s: expected a nonempty resource ID", service, name)
		}
	}
	var input AgentHostingInput
	if err := hosting.Decode(&input); err != nil {
		return err
	}
	_, err := synthesizeAgentHosting(&input, service, nil, false)
	return err
}

func synthesizeAgentHosting(
	input *AgentHostingInput, service string, env map[string]string, resolve bool,
) (*AgentHostingInput, error) {
	if input == nil {
		return nil, nil
	}
	result := *input
	for _, selection := range []struct {
		name, kind string
		value      *string
		required   bool
	}{
		{"clusterResourceId", "Microsoft.ContainerService/managedClusters", &result.ClusterResourceID, true},
		{"storageAccountResourceId", "Microsoft.Storage/storageAccounts", &result.StorageAccountResourceID, false},
		{"hostingManagementIdentityResourceId", "Microsoft.ManagedIdentity/userAssignedIdentities",
			&result.HostingManagementIdentityResourceID, false},
		{"workloadIdentityResourceId", "Microsoft.ManagedIdentity/userAssignedIdentities",
			&result.WorkloadIdentityResourceID, false},
	} {
		if *selection.value == "" && !selection.required {
			continue
		}
		value, err := resolveHostingResourceID(*selection.value,
			fmt.Sprintf("services.%s.agentHosting.%s", service, selection.name),
			selection.kind, env, resolve, selection.required)
		if err != nil {
			return nil, err
		}
		*selection.value = value
	}
	return &result, nil
}

func resolveHostingResourceID(value, field, kind string, env map[string]string, resolve, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if err := ValidateEnvReferences(value); err != nil {
		return "", fmt.Errorf("%s: %w", field, err)
	}
	if resolve {
		resolved, err := resolveVars(value, env)
		if err != nil {
			return "", fmt.Errorf("%s: %w", field, err)
		}
		value = strings.TrimSpace(resolved)
	}
	if value == "" {
		if !required {
			return "", nil
		}
		return "", fmt.Errorf("%s: required", field)
	}
	if resolve || !containsVarRef(value) {
		id, err := arm.ParseResourceID(value)
		if err != nil || id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
			!strings.EqualFold(id.ResourceType.String(), kind) ||
			len(strings.Split(value, "/")) != 9 || strings.ContainsAny(value, "?#%\\@:\x00 \t\r\n") {
			return "", fmt.Errorf("%s: expected a complete %s resource ID", field, kind)
		}
	}
	return value, nil
}
