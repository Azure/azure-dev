// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"go.yaml.in/yaml/v3"
)

// BYOAKSFeatureID identifies the existing-AKS hosting preview.
const BYOAKSFeatureID = "agents.byo_aks"

// BYOAKSEnvVar is the process-environment switch shared by Projects and Agents.
const BYOAKSEnvVar = "AZD_ALPHA_ENABLE_AGENTS_BYO_AKS"

// AgentHostingInput selects the existing cluster used to host Foundry agents.
type AgentHostingInput struct {
	ClusterResourceID string `yaml:"clusterResourceId"`
}

type agentHostingParameter struct {
	Enabled           bool   `json:"enabled"`
	HostingType       string `json:"hostingType"`
	Name              string `json:"name"`
	ClusterResourceID string `json:"clusterResourceId"`
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
