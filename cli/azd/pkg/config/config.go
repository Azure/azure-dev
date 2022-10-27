// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package config provides functionality related to storing application-wide configuration data.
//
// Configuration data stored should not be specific to a given repository/project.
package config

import (
	"fmt"
	"strings"
)

// Azd configuration for the current user
// Configuration data is stored in user's home directory @ ~/.azd/config.json
type Config interface {
	Raw() map[string]any
	Get(path string) (any, bool)
	Set(path string, value any) error
	Unset(path string) error
	IsEmpty() bool
}

// Creates a new empty configuration
func NewConfig(data map[string]any) Config {
	if data == nil {
		data = map[string]any{}
	}

	return &config{
		data: data,
	}
}

// Top level AZD configuration
type config struct {
	data map[string]any
}

// Returns a value indicating whether the configuration is empty
func (c *config) IsEmpty() bool {
	return len(c.data) == 0
}

// Gets the raw values stored in the configuration as a Go map
func (c *config) Raw() map[string]any {
	return c.data
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

	return nil
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
