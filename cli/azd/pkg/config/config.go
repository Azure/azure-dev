// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package config provides functionality related to storing application-wide configuration data.
//
// Configuration data stored should not be specific to a given repository/project.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const configDir = ".azd"

type Config interface {
	Raw() map[string]any
	Get(path string) (any, bool)
	Set(path string, value any) error
	Unset(path string) error
	Save() error
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

// GetUserConfigDir returns the config directory for storing user wide configuration data.
//
// The config directory is guaranteed to exist, otherwise an error is returned.
func GetUserConfigDir() (string, error) {
	user, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("could not determine current user: %w", err)
	}

	configDirPath := filepath.Join(user.HomeDir, configDir)
	err = os.MkdirAll(configDirPath, osutil.PermissionDirectory)

	return configDirPath, err
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

// Saves the users configuration to their local azd user folder
func (c *config) Save() error {
	configJson, err := json.MarshalIndent(c.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed marshalling config JSON: %w", err)
	}

	configPath, err := getConfigFilePath()
	if err != nil {
		return fmt.Errorf("failed locating config dir")
	}

	err = os.WriteFile(configPath, configJson, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("failed saving configuration JSON: %w", err)
	}

	return nil
}

// Loads azd configuration from the users configuration dir
func Load() (Config, error) {
	configPath, err := getConfigFilePath()
	if err != nil {
		return nil, fmt.Errorf("failed locating config dir")
	}

	bytes, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	return Parse(bytes)
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

// Parses azd configuration JSON and returns a Config instance
func Parse(configJson []byte) (Config, error) {
	var data map[string]any
	err := json.Unmarshal(configJson, &data)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling configuration JSON: %w", err)
	}

	return NewConfig(data), nil
}

func getConfigFilePath() (string, error) {
	configPath, err := GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed locating config dir")
	}

	return filepath.Join(configPath, "config.json"), nil
}

// Gets the AZD config from current context
// If it does not exist will return a new empty AZD config
func GetConfig() Config {
	azdConfig, err := Load()
	if err != nil {
		azdConfig = NewConfig(nil)
	}

	return azdConfig
}
