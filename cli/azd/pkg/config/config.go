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

//
//nolint:lll
var vaultPattern = regexp.MustCompile(
	`^vault://[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}/[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`,
)

// Azd configuration for the current user
// Configuration data is stored in user's home directory @ ~/.azd/config.json
type Config interface {
	Raw() map[string]any
	// similar to Raw() but it will resolve any vault references
	ResolvedRaw() map[string]any
	// Get retrieves the value stored at the specified path
	Get(path string) (any, bool)
	// GetString retrieves the value stored at the specified path as a string
	GetString(path string) (string, bool)
	// GetSection retrieves the value stored at the specified path and unmarshals it into the provided section
	GetSection(path string, section any) (bool, error)
	// GetMap retrieves the map stored at the specified path
	GetMap(path string) (map[string]any, bool)
	// GetSlice retrieves the slice stored at the specified path
	GetSlice(path string) ([]any, bool)
	// Set stores the value at the specified path
	Set(path string, value any) error
	// SetSecret stores the secrets at the specified path within a local user vault
	SetSecret(path string, value string) error
	// Unset removes the value stored at the specified path
	Unset(path string) error
	// IsEmpty returns a value indicating whether the configuration is empty
	IsEmpty() bool
}

// RawMapEntryConfig is an optional Config capability for accessing map entries
// without interpreting entry keys as configuration paths.
type RawMapEntryConfig interface {
	GetRawMapEntry(path string, key string) (any, bool, error)
	SetRawMapEntry(path string, key string, value any) error
	DeleteRawMapEntry(path string, key string) error
}

// GetRawMapEntry retrieves an opaque map entry without resolving vault references.
func GetRawMapEntry(config Config, path string, key string) (any, bool, error) {
	rawConfig, ok := config.(RawMapEntryConfig)
	if !ok {
		return nil, false, fmt.Errorf("config type %T does not support raw map entries", config)
	}
	return rawConfig.GetRawMapEntry(path, key)
}

// SetRawMapEntry stores an entry without interpreting its key as a configuration path.
func SetRawMapEntry(config Config, path string, key string, value any) error {
	rawConfig, ok := config.(RawMapEntryConfig)
	if !ok {
		return fmt.Errorf("config type %T does not support raw map entries", config)
	}
	return rawConfig.SetRawMapEntry(path, key, value)
}

// DeleteRawMapEntry removes an entry without interpreting its key as a configuration path.
func DeleteRawMapEntry(config Config, path string, key string) error {
	rawConfig, ok := config.(RawMapEntryConfig)
	if !ok {
		return fmt.Errorf("config type %T does not support raw map entries", config)
	}
	return rawConfig.DeleteRawMapEntry(path, key)
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

const vaultKeyName = "vault"

// Gets the raw values stored in the configuration and resolve any vault references
func (c *config) ResolvedRaw() map[string]any {
	resolvedRaw := &config{
		data: map[string]any{},
	}
	paths := paths(c.data)
	for _, path := range paths {
		if path == vaultKeyName {
			// a resolved raw should not include a reference a vault, as all secrets should be resolved
			// when a config file contains a vault reference and the vault is not found, azd returns os.ErrNotExist
			// and to the eyes of components using a Config, that means the config does not exists.
			continue
		}
		// get will always return true (no need to check) because the path was gotten from the raw config
		value, _ := c.Get(path)
		if err := resolvedRaw.Set(path, value); err != nil {
			panic(fmt.Errorf("failed setting resolved raw value: %w", err))
		}
	}
	return resolvedRaw.data
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

// SetSecret stores the secrets at the specified path within a local user vault
func (c *config) SetSecret(path string, value string) error {
	if c.vaultId == "" {
		c.vault = NewConfig(nil)
		c.vaultId = uuid.New().String()
		if err := c.Set(vaultKeyName, c.vaultId); err != nil {
			return fmt.Errorf("failed setting vault id: %w", err)
		}
	}

	pathId := uuid.New().String()
	vaultRef := fmt.Sprintf("vault://%s/%s", c.vaultId, pathId)
	if err := c.vault.Set(pathId, base64.StdEncoding.EncodeToString([]byte(value))); err != nil {
		return fmt.Errorf("failed setting secret value: %w", err)
	}

	return c.Set(path, vaultRef)
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

// GetMap retrieves the map stored at the specified path
func (c *config) GetMap(path string) (map[string]any, bool) {
	value, ok := c.Get(path)
	if !ok {
		return nil, false
	}

	node, ok := value.(map[string]any)
	return node, ok
}

func (c *config) GetRawMapEntry(path string, key string) (any, bool, error) {
	node, found, err := c.rawMap(path, false)
	if err != nil || !found {
		return nil, false, err
	}

	value, found := node[key]
	return value, found, nil
}

func (c *config) SetRawMapEntry(path string, key string, value any) error {
	node, _, err := c.rawMap(path, true)
	if err != nil {
		return err
	}

	node[key] = value
	return nil
}

func (c *config) DeleteRawMapEntry(path string, key string) error {
	node, found, err := c.rawMap(path, false)
	if err != nil || !found {
		return err
	}

	delete(node, key)
	return nil
}

func (c *config) rawMap(path string, create bool) (map[string]any, bool, error) {
	if path == "" {
		return c.data, true, nil
	}

	currentNode := c.data
	for part := range strings.SplitSeq(path, ".") {
		value, exists := currentNode[part]
		if !exists || value == nil {
			if !create {
				return nil, false, nil
			}
			node := map[string]any{}
			currentNode[part] = node
			currentNode = node
			continue
		}

		node, ok := value.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("failed converting node at path '%s' to map", part)
		}
		currentNode = node
	}

	return currentNode, true, nil
}

// GetSlice retrieves the slice stored at the specified path
func (c *config) GetSlice(path string) ([]any, bool) {
	value, ok := c.Get(path)
	if !ok {
		return nil, false
	}

	node, ok := value.([]any)
	return node, ok
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
func (c *config) interpolateNodeValue(value any) (any, bool) {
	// Check if the value is a vault reference
	// If it is, retrieve the secret from the vault
	if vaultRef, isString := value.(string); isString && vaultPattern.MatchString(vaultRef) {
		return c.getSecret(vaultRef)
	}

	// If the value is a map, recursively iterate over the map and interpolate the values
	if node, isMap := value.(map[string]any); isMap {
		// We want to ensure we return a cloned map so that we don't modify the original data
		// stored within the config map data structure
		cloneMap := map[string]any{}

		for key, val := range node {
			if nodeValue, ok := c.interpolateNodeValue(val); ok {
				cloneMap[key] = nodeValue
			}
		}

		return cloneMap, true
	}

	// Finally, if the value is not handled above we can return the value as is
	return value, true
}
