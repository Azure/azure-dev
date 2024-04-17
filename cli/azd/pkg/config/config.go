// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package config provides functionality related to storing application-wide configuration data.
//
// Configuration data stored should not be specific to a given repository/project.
package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Azd configuration for the current user
// Configuration data is stored in user's home directory @ ~/.azd/config.json
type Config interface {
	Raw() map[string]any
	Paths() []string
	Get(path string) (any, bool)
	GetString(path string) (string, bool)
	GetSection(path string, section any) (bool, error)
	Set(path string, value any) error
	SetSecret(path string, value any) error
	SecretKeys() map[string]struct{}
	Unset(path string) error
	IsEmpty() bool
}

// NewEmptyConfig creates a empty configuration object.
func NewEmptyConfig() Config {
	return NewConfig(nil)
}

// NewConfig creates a configuration object, populated with an initial set of keys and values. If [data] is nil or an
// empty map, and empty configuration object is returned, but [NewEmptyConfig] might better express your intention.
func NewConfig(data map[string]any) Config {
	if data == nil {
		data = map[string]any{}
	}

	return &config{
		data:    data,
		secrets: map[string]struct{}{},
	}
}

// Top level AZD configuration
type config struct {
	data    map[string]any
	secrets map[string]struct{}
}

// Returns a value indicating whether the configuration is empty
func (c *config) IsEmpty() bool {
	return len(c.data) == 0
}

// Gets the raw values stored in the configuration as a Go map
func (c *config) Raw() map[string]any {
	return c.data
}

// Paths returns the list of paths from the configuration.
func (c *config) Paths() []string {
	return paths(c.data)
}

// paths recursively traverses a map and returns a list of all the paths to the leaf nodes.
// The start parameter is the initial map to start traversing from.
// It returns a slice of strings representing the paths to the leaf nodes.
func paths(start map[string]any) []string {
	var all []string
	for path, value := range start {
		if node, isNode := value.(map[string]any); isNode {
			for _, child := range paths(node) {
				all = append(all, fmt.Sprintf("%s.%s", path, child))
			}
		} else {
			all = append(all, path)
		}
	}
	return all
}

// Sets a value at the specified location
func (c *config) Set(path string, value any) error {
	depth := 1
	currentNode := c.data
	parts := strings.Split(path, ".")
	for _, part := range parts {
		if depth == len(parts) {
			currentNode[part] = value
			return nil
		}
		var node map[string]any
		value, ok := currentNode[part]
		if !ok || value == nil {
			node = map[string]any{}
		}

		if value != nil {
			node, ok = value.(map[string]any)
			if !ok {
				return fmt.Errorf("failed converting node at path '%s' to map", part)
			}
		}

		currentNode[part] = node
		currentNode = node
		depth++
	}
	// make sure calling set overrides a path which might have been marked as a secret
	delete(c.secrets, path)
	return nil
}

// Sets a value at the specified location
func (c *config) SetSecret(path string, value any) error {
	if err := c.Set(path, value); err != nil {
		return err
	}
	c.secrets[path] = struct{}{}
	return nil
}

func (c *config) SecretKeys() map[string]struct{} {
	return c.secrets
}

// Removes any values stored at the specified path
// When the path location is an object will remove the whole node
// When the path does not exist, will return a `nil` value
func (c *config) Unset(path string) error {
	depth := 1
	currentNode := c.data
	parts := strings.Split(path, ".")
	for _, part := range parts {
		if depth == len(parts) {
			delete(currentNode, part)
			return nil
		}
		var node map[string]any
		value, ok := currentNode[part]

		// Path already doesn't exist, NOOP
		if !ok || value == nil {
			return nil
		}

		if value != nil {
			node, ok = value.(map[string]any)
			if !ok {
				return fmt.Errorf("failed converting node at path '%s' to map", part)
			}
		}

		currentNode[part] = node
		currentNode = node
		depth++
	}

	return nil
}

// Gets the value stored at the specified location
// Returns the value if exists, otherwise returns nil & a value indicating if the value existing
func (c *config) Get(path string) (any, bool) {
	depth := 1
	currentNode := c.data
	parts := strings.Split(path, ".")
	for _, part := range parts {
		if depth == len(parts) {
			value, ok := currentNode[part]
			return value, ok
		}
		value, ok := currentNode[part]
		if !ok {
			return value, ok
		}

		node, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}

		currentNode = node
		depth++
	}

	return nil, false
}

// Gets the value stored at the specified location as a string
func (c *config) GetString(path string) (string, bool) {
	value, ok := c.Get(path)
	if !ok {
		return "", false
	}

	str, ok := value.(string)
	return str, ok
}

func (c *config) GetSection(path string, section any) (bool, error) {
	sectionConfig, ok := c.Get(path)
	if !ok {
		return false, nil
	}

	jsonBytes, err := json.Marshal(sectionConfig)
	if err != nil {
		return true, fmt.Errorf("marshalling section config: %w", err)
	}

	if err := json.Unmarshal(jsonBytes, section); err != nil {
		return true, fmt.Errorf("unmarshalling section config: %w", err)
	}

	return true, nil
}
