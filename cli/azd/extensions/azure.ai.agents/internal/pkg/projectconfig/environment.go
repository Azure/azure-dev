// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"go.yaml.in/yaml/v3"
)

// LoadServiceEnvironment reads raw env values from azure.yaml.
func LoadServiceEnvironment(
	projectRoot string,
	serviceName string,
) (map[string]string, error) {
	if projectRoot == "" || serviceName == "" {
		return nil, nil
	}

	data, path, err := ReadProjectFile(projectRoot)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	var document struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	entry := document.Services[serviceName]
	if entry == nil {
		return nil, nil
	}
	resolved, err := foundry.ResolveFileRefs(entry, projectRoot)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve service %q config: %w",
			serviceName,
			err,
		)
	}
	if err := NormalizeEnvironment(resolved); err != nil {
		return nil, fmt.Errorf("service %q: %w", serviceName, err)
	}
	value, exists := resolved["env"]
	if !exists {
		return nil, nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(
			"service %q env must be a mapping",
			serviceName,
		)
	}

	env := make(map[string]string, len(raw))
	for key, value := range raw {
		text, err := scalarString(value)
		if err != nil {
			return nil, fmt.Errorf(
				"service %q env %q: %w",
				serviceName,
				key,
				err,
			)
		}
		env[key] = text
	}
	return env, nil
}

// NormalizeEnvironment converts scalar env values to strings in-place.
func NormalizeEnvironment(properties map[string]any) error {
	value, exists := properties["env"]
	if !exists {
		return nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("env must be a mapping")
	}
	for key, value := range raw {
		text, err := scalarString(value)
		if err != nil {
			return fmt.Errorf("env %q: %w", key, err)
		}
		raw[key] = text
	}
	return nil
}

// ReadProjectFile loads the project's azure.yaml or azure.yml.
func ReadProjectFile(projectRoot string) ([]byte, string, error) {
	for _, name := range []string{"azure.yaml", "azure.yml"} {
		path := filepath.Join(projectRoot, name)
		data, err := os.ReadFile(path) //nolint:gosec
		if err == nil {
			return data, path, nil
		}
		if !os.IsNotExist(err) {
			return nil, path, fmt.Errorf("read %s: %w", path, err)
		}
	}
	return nil, "", nil
}

func scalarString(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	kind := reflect.TypeOf(value).Kind()
	if kind == reflect.Map ||
		kind == reflect.Slice ||
		kind == reflect.Array {
		return "", fmt.Errorf("must be a scalar")
	}
	return fmt.Sprint(value), nil
}
