package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type userSecretsManager struct {
	manager FileConfigManager
}

func newUserSecretsManager(configManager FileConfigManager) UserConfigManager {
	return &userSecretsManager{
		manager: configManager,
	}
}

func (m *userSecretsManager) Load() (Config, error) {
	var userVault Config

	secretsFilePath, err := getUserSecretsFilePath()
	if err != nil {
		return nil, err
	}

	userVault, err = m.manager.Load(secretsFilePath)
	if err != nil {
		// Ignore missing file errors
		// File will automatically be created on first `set` operation
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("creating empty vault since '%s' did not exist.", secretsFilePath)
			return NewConfig(nil), nil
		}

		return nil, fmt.Errorf("failed loading user vault from '%s'. %w", secretsFilePath, err)
	}

	return userVault, nil
}

func (m *userSecretsManager) Save(c Config) error {
	userSecretsFilePath, err := getUserSecretsFilePath()
	if err != nil {
		return fmt.Errorf("failed getting user vault file path. %w", err)
	}

	err = m.manager.Save(c, userSecretsFilePath)
	if err != nil {
		return fmt.Errorf("failed saving vault. %w", err)
	}

	return nil
}

func getUserSecretsFilePath() (string, error) {
	configPath, err := GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed getting user vault file path '%s'. %w", configPath, err)
	}

	return filepath.Join(configPath, ".secrets", "vault.json"), nil
}
