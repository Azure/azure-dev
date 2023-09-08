package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type UserConfigManager interface {
	Save(Config) error
	Load() (Config, error)
}

type userConfigManager struct {
	manager FileConfigManager
}

func NewUserConfigManager(configManager FileConfigManager) UserConfigManager {
	return &userConfigManager{
		manager: configManager,
	}
}

func (m *userConfigManager) Load() (Config, error) {
	var azdConfig Config

	configFilePath, err := GetUserConfigFilePath()
	if err != nil {
		return nil, err
	}

	azdConfig, err = m.manager.Load(configFilePath)
	if err != nil {
		// Ignore missing file errors
		// File will automatically be created on first `set` operation
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("creating empty config since '%s' did not exist.", configFilePath)
			return NewConfig(nil), nil
		}

		return nil, fmt.Errorf("failed loading azd user config from '%s'. %w", configFilePath, err)
	}

	return azdConfig, nil
}

func (m *userConfigManager) Save(c Config) error {
	userConfigFilePath, err := GetUserConfigFilePath()
	if err != nil {
		return fmt.Errorf("failed getting user config file path. %w", err)
	}

	err = m.manager.Save(c, userConfigFilePath)
	if err != nil {
		return fmt.Errorf("failed saving configuration. %w", err)
	}

	return nil
}

// Gets the local file system path to the Azd configuration file
func GetUserConfigFilePath() (string, error) {
	configPath, err := GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed getting user config file path '%s'. %w", configPath, err)
	}

	return filepath.Join(configPath, "config.json"), nil
}
