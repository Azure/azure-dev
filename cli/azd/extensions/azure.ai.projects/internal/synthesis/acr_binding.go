// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ACRBindingConfigKey stores deployed facts in environment config.
const ACRBindingConfigKey = "extensions.ai-projects.acrBinding"

// ACRBindingEnvKey carries a binding in a synthesis snapshot.
const ACRBindingEnvKey = "AZD_FOUNDRY_ACR_BINDING"

var projectResourceIDPattern = regexp.MustCompile(
	`(?i)^/subscriptions/[^/\s?#\\%]+/resourceGroups/[^/\s?#\\%]+/providers/` +
		`Microsoft\.CognitiveServices/accounts/[^/\s?#\\%]+/projects/[^/\s?#\\%]+$`,
)

var acrOwnershipTokenPattern = regexp.MustCompile(
	`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// ACRAssignment identifies an environment-owned access grant.
type ACRAssignment struct {
	ID               string   `json:"id"`
	PrincipalID      string   `json:"principalId"`
	RoleDefinitionID string   `json:"roleDefinitionId"`
	OwnershipToken   string   `json:"ownershipToken"`
	DeploymentIDs    []string `json:"deploymentIds,omitempty"`
}

// ACRBinding records deployed registry facts, not setup intent.
type ACRBinding struct {
	Version            int             `json:"version"`
	Mode               string          `json:"mode"`
	RegistryResourceID string          `json:"registryResourceId"`
	ProjectResourceID  string          `json:"projectResourceId"`
	PrincipalID        string          `json:"principalId"`
	DeploymentSalt     string          `json:"deploymentSalt,omitempty"`
	OwnedAssignments   []ACRAssignment `json:"ownedAssignments,omitempty"`
}

// ParseACRBinding rejects malformed persisted deployment facts.
func ParseACRBinding(value any) (*ACRBinding, error) {
	if value == nil {
		return nil, nil
	}
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case json.RawMessage:
		data = v
	case string:
		data = []byte(v)
	default:
		var err error
		data, err = json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode ACR binding: %w", err)
		}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var binding ACRBinding
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil {
		return nil, fmt.Errorf("invalid deployed ACR binding: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid deployed ACR binding: expected one object")
	}
	if binding.Version != 1 || (binding.Mode != "create" && binding.Mode != "reuse-connect") ||
		!containerRegistryIDPattern.MatchString(binding.RegistryResourceID) ||
		!projectResourceIDPattern.MatchString(binding.ProjectResourceID) || binding.PrincipalID == "" {
		return nil, errors.New("invalid deployed ACR binding: incomplete or unsupported deployment facts")
	}
	for _, assignment := range binding.OwnedAssignments {
		expectedPrefix := binding.RegistryResourceID + "/providers/Microsoft.Authorization/roleAssignments/"
		parts := strings.Split(binding.RegistryResourceID, "/")
		rolePrefix := "/subscriptions/" + parts[2] + "/providers/Microsoft.Authorization/roleDefinitions/"
		knownRole := strings.EqualFold(assignment.RoleDefinitionID, rolePrefix+"7f951dda-4ed3-4680-a7ca-43fe172d538d") ||
			strings.EqualFold(assignment.RoleDefinitionID, rolePrefix+"b93aa761-3e63-49ed-ac28-beffa264f7ac")
		if !strings.EqualFold(assignment.ID, expectedPrefix+assignment.OwnershipToken) ||
			!acrOwnershipTokenPattern.MatchString(assignment.OwnershipToken) ||
			!strings.EqualFold(assignment.PrincipalID, binding.PrincipalID) ||
			!knownRole {
			return nil, errors.New("invalid deployed ACR binding: incomplete owned assignment")
		}
		deploymentScope := strings.Join(parts[:5], "/") + "/providers/Microsoft.Resources/deployments/"
		expected := []string{
			deploymentScope + "azd-acr-access-" + assignment.OwnershipToken,
			deploymentScope + "azd-acr-" + assignment.OwnershipToken,
		}
		if len(assignment.DeploymentIDs) != len(expected) {
			return nil, errors.New("invalid deployed ACR binding: incomplete deployment ownership")
		}
		for i, id := range expected {
			if !strings.EqualFold(id, assignment.DeploymentIDs[i]) {
				return nil, errors.New("invalid deployed ACR binding: mismatched deployment ownership")
			}
		}
	}
	return &binding, nil
}

// ValidateACRBinding rejects migration of a deployed registry.
func ValidateACRBinding(binding *ACRBinding, mode, registryID string) error {
	if binding == nil {
		return nil
	}
	if mode == "" {
		mode = binding.Mode
	}
	if mode != binding.Mode ||
		(mode == "reuse-connect" && !strings.EqualFold(binding.RegistryResourceID, registryID)) ||
		(mode == "create" && registryID != "" && !strings.EqualFold(binding.RegistryResourceID, registryID)) {
		return fmt.Errorf("registry migration is not supported for an already provisioned Foundry project; "+
			"retain the deployed %s registry %q or use a new azd environment", binding.Mode, binding.RegistryResourceID)
	}
	return nil
}

// ValidateACRSelector validates a registry before writes.
func ValidateACRSelector(registryID, subscriptionID, resourceGroup string) error {
	if !containerRegistryIDPattern.MatchString(registryID) {
		return errors.New("AZURE_CONTAINER_REGISTRY_RESOURCE_ID must be a full ARM ID " +
			"of a Microsoft.ContainerRegistry/registries resource")
	}
	parts := strings.Split(registryID, "/")
	if subscriptionID != "" && !strings.EqualFold(parts[2], subscriptionID) {
		return errors.New("AZURE_CONTAINER_REGISTRY_RESOURCE_ID must use the same subscription " +
			"as the Foundry project (AZURE_SUBSCRIPTION_ID); cross-subscription registry reuse is not supported")
	}
	if subscriptionID != "" && resourceGroup != "" &&
		strings.EqualFold(parts[2], subscriptionID) && strings.EqualFold(parts[4], resourceGroup) {
		return errors.New("AZURE_CONTAINER_REGISTRY_RESOURCE_ID must be outside the Foundry " +
			"resource group because azd down deletes that group")
	}
	return nil
}

// ValidateGreenfieldACRSelection validates a setup selector.
func ValidateGreenfieldACRSelection(env map[string]string, selectedID string) error {
	group := env["AZURE_FOUNDRY_RESOURCE_GROUP"]
	if group == "" {
		group = env["AZURE_RESOURCE_GROUP"]
	}
	if err := ValidateACRSelector(selectedID, env["AZURE_SUBSCRIPTION_ID"], group); err != nil {
		return err
	}
	binding, err := ParseACRBinding(env[ACRBindingEnvKey])
	if err != nil {
		return err
	}
	return ValidateACRBinding(binding, "reuse-connect", selectedID)
}

// GreenfieldACRResourceGroup resolves the actual teardown group.
func GreenfieldACRResourceGroup(in Input) (string, error) {
	if in.ResourceGroupName != "" {
		return in.ResourceGroupName, nil
	}
	var doc struct {
		Infra struct {
			Provider string `yaml:"provider"`
			Layers   []struct {
				Name     string `yaml:"name"`
				Provider string `yaml:"provider"`
			} `yaml:"layers"`
		} `yaml:"infra"`
	}
	if err := yaml.Unmarshal(in.RawAzureYAML, &doc); err != nil {
		return "", fmt.Errorf("parse infrastructure scope in azure.yaml: %w", err)
	}
	layered := false
	for _, layer := range doc.Infra.Layers {
		provider := strings.TrimSpace(layer.Provider)
		if provider == "" {
			provider = strings.TrimSpace(doc.Infra.Provider)
		}
		if provider != "microsoft.foundry" {
			continue
		}
		if strings.TrimSpace(layer.Name) == "" || layered {
			return "", errors.New("registry selection requires one named Foundry infrastructure layer")
		}
		layered = true
	}
	key, suffix := "AZURE_RESOURCE_GROUP", ""
	if layered {
		key, suffix = "AZURE_FOUNDRY_RESOURCE_GROUP", "-foundry"
	}
	if group := in.Env[key]; group != "" {
		return group, nil
	}
	if name := in.Env["AZURE_ENV_NAME"]; name != "" {
		return "rg-" + name + suffix, nil
	}
	return "", nil
}

// ValidateGreenfieldACRSelectionForInput guards setup and eject.
func ValidateGreenfieldACRSelectionForInput(in Input, selectedID string) error {
	_, projects, err := acrProjectServices(in)
	if err != nil {
		return err
	}
	for _, project := range projects {
		if strings.TrimSpace(project.Endpoint) != "" {
			return errors.New("--acr-resource-id is only supported for a new Foundry project; " +
				"use the existing project's registry connection instead of a source-build selector")
		}
	}
	group, err := GreenfieldACRResourceGroup(in)
	if err != nil {
		return err
	}
	subscription := in.Env["AZURE_SUBSCRIPTION_ID"]
	if err := ValidateACRSelector(selectedID, subscription, group); err != nil {
		return err
	}
	if subscription == "" && group != "" && strings.EqualFold(strings.Split(selectedID, "/")[4], group) {
		return errors.New("cannot establish that the registry is outside the Foundry resource group; " +
			"configure AZURE_SUBSCRIPTION_ID before selecting a registry with the same resource-group name")
	}
	binding := in.ACRBinding
	if binding == nil {
		binding, err = ParseACRBinding(in.Env[ACRBindingEnvKey])
		if err != nil {
			return err
		}
	}
	return ValidateACRBinding(binding, "reuse-connect", selectedID)
}
