// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package config provides functionality related to storing application-wide configuration data.
//
// Configuration data stored should not be specific to a given repository/project.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const configDir = ".azd"

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

type Config struct {
	DefaultSubscription *Subscription `json:"defaultSubscription"`
	DefaultLocation     *Location     `json:"defaultLocation"`
}

type Subscription struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

type Location struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// Saves the users configuration to their local azd user folder
func (c *Config) Save() error {
	configJson, err := json.Marshal(*c)
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
func Load() (*Config, error) {
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

// Parses azd configuration JSON and returns a Config instance
func Parse(configJson []byte) (*Config, error) {
	var config Config
	err := json.Unmarshal(configJson, &config)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling configuration JSON: %w", err)
	}

	return &config, nil
}

func getConfigFilePath() (string, error) {
	configPath, err := GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed locating config dir")
	}

	return filepath.Join(configPath, "config.json"), nil
}

type contextKey string

const configContextKey contextKey = "config"

// Gets the AZD config from current context
// If it does not exist will return a new empty AZD config
func GetConfig(ctx context.Context) *Config {
	config, ok := ctx.Value(configContextKey).(*Config)
	if !ok {
		loadedConfig, err := Load()
		if err != nil {
			loadedConfig = &Config{}
		}
		config = loadedConfig
	}

	return config
}

// Sets the AZD config in the Go context and returns the new context
func WithConfig(ctx context.Context, config *Config) context.Context {
	return context.WithValue(ctx, configContextKey, config)
}
