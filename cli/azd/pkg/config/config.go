// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package config provides functionality related to storing application-wide configuration data.
//
// Configuration data stored should not be specific to a given repository/project.
package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

//nolint:lll
var VaultPattern = regexp.MustCompile(
	`^vault://[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}/[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`,
)

// Azd configuration for the current user
// Configuration data is stored in user's home directory @ ~/.azd/config.json
type Config interface {
	Raw() map[string]any
	Get(path string) (any, bool)
	GetString(path string) (string, bool)
	GetSection(path string, section any) (bool, error)
	Set(path string, value any) error
	SetSecret(path string, value string) (string, error)
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
		data: data,
	}
}

// Top level AZD configuration
type config struct {
	vaultId string
	vault   Config
	data    map[string]any
}

// Returns a value indicating whether the configuration is empty
func (c *config) IsEmpty() bool {
	return len(c.data) == 0
}

// Gets the raw values stored in the configuration as a Go map
func (c *config) Raw() map[string]any {
	return c.data
}

// SetSecret stores the secrets at the specified path within a local user vault
func (c *config) SetSecret(path string, value string) (string, error) {
	if c.vaultId == "" {
		c.vault = NewConfig(nil)
		c.vaultId = uuid.New().String()
		if err := c.Set("vault", c.vaultId); err != nil {
			return "", fmt.Errorf("failed setting vault id: %w", err)
		}
	}

	secretId := uuid.New().String()
	vaultRef := fmt.Sprintf("vault://%s/%s", c.vaultId, secretId)
	if err := c.vault.Set(secretId, base64.StdEncoding.EncodeToString([]byte(value))); err != nil {
		return "", fmt.Errorf("failed setting secret value: %w", err)
	}

	if err := c.Set(path, vaultRef); err != nil {
		return "", fmt.Errorf("failed setting secret reference: %w", err)
	}

	return vaultRef, nil
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
			if err := c.deletedNestedSecrets(currentNode[part]); err != nil {
				return err
			}

			delete(currentNode, part)
			return nil
		}
		var node map[string]any
		value, ok := currentNode[part]

		// Path already doesn't exist, NOOP
		if !ok || value == nil {
			return nil
		}

		node, ok = value.(map[string]any)
		if !ok {
			return fmt.Errorf("failed converting node at path '%s' to map", part)
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
		// When the depth is equal to the number of parts, we have reached the desired node path
		// At this point we can perform any final processing on the node and return the result
		if depth == len(parts) {
			value, ok := currentNode[part]
			if !ok {
				return value, ok
			}

			return c.interpolateNodeValue(value)
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

// getSecret retrieves the secret stored at the specified path from a local user vault
func (c *config) getSecret(vaultRef string) (string, bool) {
	encodedValue, ok := c.vault.GetString(filepath.Base(vaultRef))
	if !ok {
		return "", false
	}

	bytes, err := base64.StdEncoding.DecodeString(encodedValue)
	if err != nil {
		return "", false
	}

	return string(bytes), true
}

// interpolateNodeValue processes the node, iterates on any nested nodes and interpolates any vault references
func (c *config) interpolateNodeValue(node any) (any, bool) {
	// Check if the value is a vault reference
	// If it is, retrieve the secret from the vault
	if vaultRef, isString := node.(string); isString && VaultPattern.MatchString(vaultRef) {
		return c.getSecret(vaultRef)
	}

	// If the value is a map, recursively iterate over the map and interpolate the values
	if node, isMap := node.(map[string]any); isMap {
		// We want to ensure we return a cloned map so that we don't modify the original data
		// stored within the config map data structure
		cloneMap := map[string]any{}

		for key, childNode := range node {
			if nodeValue, ok := c.interpolateNodeValue(childNode); ok {
				cloneMap[key] = nodeValue
			}
		}

		return cloneMap, true
	}

	// Finally, if the value is not handled above we can return the value as is
	return node, true
}

// deletedNestedSecrets iterates over the node and removes any nested referenced by any child nodes
func (c *config) deletedNestedSecrets(node any) error {
	if vaultRef, isString := node.(string); isString && VaultPattern.MatchString(vaultRef) {
		return c.vault.Unset(filepath.Base(vaultRef))
	}

	if node, isMap := node.(map[string]any); isMap {
		for _, childNode := range node {
			if err := c.deletedNestedSecrets(childNode); err != nil {
				return err
			}
		}
	}

	return nil
}
