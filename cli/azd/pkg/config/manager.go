package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const configDir = ".azd"

// Config Manager provides the ability to load, parse and save azd configuration files
type manager struct {
}

type Manager interface {
	Save(config Config, filePath string) error
	Load(filePath string) (Config, error)
	Parse(configJson []byte) (Config, error)
}

// Creates a new Configuration Manager
func NewManager() Manager {
	return &manager{}
}

// Saves the azd configuration to the specified file path
func (c *manager) Save(config Config, filePath string) error {
	configJson, err := json.MarshalIndent(config.Raw(), "", "  ")
	if err != nil {
		return fmt.Errorf("failed marshalling config JSON: %w", err)
	}

	err = os.WriteFile(filePath, configJson, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("failed writing configuration data")
	}

	return nil
}

// Loads azd configuration from the specified file path
func (c *manager) Load(filePath string) (Config, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed opening azd configuration file: %w", err)
	}

	defer file.Close()

	jsonBytes, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed reading azd configuration file")
	}

	return c.Parse(jsonBytes)
}

// Parses azd configuration JSON and returns a Config instance
func (c *manager) Parse(configJson []byte) (Config, error) {
	var data map[string]any
	err := json.Unmarshal(configJson, &data)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling configuration JSON: %w", err)
	}

	return NewConfig(data), nil
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

// Gets the local file system path to the Azd configuration file
func GetUserConfigFilePath() (string, error) {
	configPath, err := GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed getting user config file path '%s'. %w", configPath, err)
	}

	return filepath.Join(configPath, "config.json"), nil
}

// GetUserConfig gets the Config for the current user, given a manager. If the config file does not exist, an empty
// configuration object is returned.
func GetUserConfig(configManager Manager) (Config, error) {
	var azdConfig Config

	configFilePath, err := GetUserConfigFilePath()
	if err != nil {
		return nil, err
	}

	azdConfig, err = configManager.Load(configFilePath)
	if err != nil {
		// Ignore missing file errors
		// File will automatically be created on first `set` operation
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("failed loading azd user config from '%s'. %s\n", configFilePath, err.Error())
			return NewConfig(nil), nil
		}

		return nil, fmt.Errorf("failed loading azd user config from '%s'. %w", configFilePath, err)
	}

	return azdConfig, nil
}
