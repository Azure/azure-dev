// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// AgentHostingInput contains only supported customer configuration.
type AgentHostingInput struct {
	HostingType              string
	Name                     string
	ClusterResourceID        string
	StorageAccountResourceID string
	AgentSubnetResourceID    string
}

// PreparedHosting contains private, prepared deployment dependencies.
type PreparedHosting struct {
	HostingManagementIdentityResourceID string
	WorkloadIdentityResourceID          string
	StorageAccountResourceID            string
	AgentSubnetResourceID               string
	RegistryResourceID                  string
	RegistryABAC                        bool
}

func (h *agentHostingBlock) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("agentHosting must be an object")
	}
	h.present = map[string]bool{}
	values := map[string]*string{
		"hostingType": &h.HostingType, "name": &h.Name, "clusterResourceId": &h.ClusterResourceID,
		"storageAccountResourceId": &h.StorageAccountResourceID, "agentSubnetResourceId": &h.AgentSubnetResourceID,
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		if key == "hostingManagementIdentityResourceId" || key == "workloadIdentityResourceId" {
			return fmt.Errorf("agentHosting.%s is not supported; remove this property; azd manages both identities", key)
		}
		out, ok := values[key]
		if !ok {
			return fmt.Errorf("agentHosting.%s is not supported", key)
		}
		if h.present[key] {
			return fmt.Errorf("agentHosting.%s is duplicated", key)
		}
		h.present[key] = true
		if value.Tag == "!!null" || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return fmt.Errorf("agentHosting.%s must be a nonempty string", key)
		}
		*out = value.Value
	}
	return nil
}

func resolveOptionalHostingValue(
	value string, present bool, field string, env map[string]string, resolve bool, pattern *regexp.Regexp,
) (string, error) {
	if !present && value == "" {
		return "", nil
	}
	return resolveAgentHostingValue(value, field, env, resolve, pattern)
}

// ApplyPreparedHosting completes internal parameters immediately before deployment.
func ApplyPreparedHosting(result *Result, prepared PreparedHosting) error {
	if result == nil || result.Hosting == nil {
		return fmt.Errorf("prepared hosting requires a BYO-AKS synthesis result")
	}
	hosting, ok := result.Parameters["agentHosting"].(agentHostingParameter)
	if !ok || !hosting.Enabled {
		return fmt.Errorf("prepared hosting requires enabled internal hosting parameters")
	}
	values := map[string]string{
		"hostingManagementIdentityResourceId": prepared.HostingManagementIdentityResourceID,
		"workloadIdentityResourceId":          prepared.WorkloadIdentityResourceID,
		"storageAccountResourceId":            prepared.StorageAccountResourceID,
		"agentSubnetResourceId":               prepared.AgentSubnetResourceID,
	}
	for key, value := range values {
		pattern := agentHostingResourceIDPatterns[key]
		if strings.TrimSpace(value) == "" || !pattern.MatchString(value) {
			return fmt.Errorf("prepared %s must be a complete resource ID", key)
		}
	}
	if strings.EqualFold(prepared.HostingManagementIdentityResourceID, prepared.WorkloadIdentityResourceID) {
		return fmt.Errorf("prepared hosting and workload identities must be distinct")
	}
	registryPattern := regexp.MustCompile(
		`(?i)^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.ContainerRegistry/registries/[^/]+$`,
	)
	if !registryPattern.MatchString(prepared.RegistryResourceID) {
		return fmt.Errorf("prepared registry must be a complete Microsoft.ContainerRegistry/registries resource ID")
	}
	if result.Hosting.StorageAccountResourceID != "" &&
		!strings.EqualFold(result.Hosting.StorageAccountResourceID, prepared.StorageAccountResourceID) {
		return fmt.Errorf("prepared storage must preserve the explicitly supplied storage account")
	}
	if result.Hosting.AgentSubnetResourceID != "" &&
		!strings.EqualFold(result.Hosting.AgentSubnetResourceID, prepared.AgentSubnetResourceID) {
		return fmt.Errorf("prepared subnet must preserve the explicitly supplied agent subnet")
	}
	hosting.HostingManagementIdentityResourceID = prepared.HostingManagementIdentityResourceID
	hosting.WorkloadIdentityResourceID = prepared.WorkloadIdentityResourceID
	hosting.StorageAccountResourceID = prepared.StorageAccountResourceID
	hosting.AgentSubnetResourceID = prepared.AgentSubnetResourceID
	result.Parameters["agentHosting"] = hosting
	result.Parameters["containerRegistryResourceId"] = prepared.RegistryResourceID
	result.Parameters["containerRegistryABAC"] = prepared.RegistryABAC
	result.Parameters["includeAcr"] = true
	return nil
}
