// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DeploymentBindingsEnvKey stores explicit per-declaration selections.
const DeploymentBindingsEnvKey = "AI_PROJECT_DEPLOYMENT_BINDINGS"

// DecodeDeploymentBindings reads raw or environment-escaped JSON.
// An empty target records an explicitly skipped declaration.
func DecodeDeploymentBindings(value string) (map[string]string, error) {
	bindings := map[string]string{}
	value = strings.TrimSpace(value)
	if value == "" {
		return bindings, nil
	}
	if !json.Valid([]byte(value)) {
		var unescaped string
		if err := json.Unmarshal([]byte(`"`+value+`"`), &unescaped); err != nil {
			return nil, fmt.Errorf("decoding %s: %w", DeploymentBindingsEnvKey, err)
		}
		value = unescaped
	}
	if err := json.Unmarshal([]byte(value), &bindings); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", DeploymentBindingsEnvKey, err)
	}
	if bindings == nil {
		return nil, fmt.Errorf("%s must be a JSON object", DeploymentBindingsEnvKey)
	}
	for name := range bindings {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%s contains an empty declaration name", DeploymentBindingsEnvKey)
		}
	}
	return bindings, nil
}

// SetDeploymentBinding updates case-equivalent declaration keys without changing
// their spelling, or adds the declaration when no matching key exists.
func SetDeploymentBinding(bindings map[string]string, declaration, target string) {
	matched := false
	for name := range bindings {
		if strings.EqualFold(name, declaration) {
			bindings[name] = target
			matched = true
		}
	}
	if !matched {
		bindings[declaration] = target
	}
}

// EncodeDeploymentBindings writes environment-escaped JSON selections.
func EncodeDeploymentBindings(bindings map[string]string) (string, error) {
	data, err := json.Marshal(bindings)
	if err != nil {
		return "", fmt.Errorf("encoding %s: %w", DeploymentBindingsEnvKey, err)
	}
	escaped := strings.ReplaceAll(string(data), "\\", "\\\\")
	return strings.ReplaceAll(escaped, "\"", "\\\""), nil
}
