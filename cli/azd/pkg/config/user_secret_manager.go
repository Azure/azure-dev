package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type envVaultManager struct {
	vaultId string
	manager FileConfigManager
}

func newEnvVaultManager(vaultId string, configManager FileConfigManager) UserConfigManager {
	return &envVaultManager{
		manager: configManager,
		vaultId: vaultId,
	}
}

func (m *envVaultManager) Load() (Config, error) {
	var userVault Config

	vaultsFilePath, err := getUserSecretsFilePath(m.vaultId)
	if err != nil {
		return nil, err
	}

	userVault, err = m.manager.Load(vaultsFilePath)
	if err != nil {
		// Ignore missing file errors
		// File will automatically be created on first `set` operation
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("creating empty vault since '%s' did not exist.", vaultsFilePath)
			return NewConfig(nil), nil
		}

		return nil, fmt.Errorf("failed loading user vault from '%s'. %w", vaultsFilePath, err)
	}

	return userVault, nil
}

func (m *envVaultManager) Save(c Config) error {
	userSecretsFilePath, err := getUserSecretsFilePath(m.vaultId)
	if err != nil {
		return fmt.Errorf("failed getting user vault file path. %w", err)
	}

	err = m.manager.Save(c, userSecretsFilePath)
	if err != nil {
		return fmt.Errorf("failed saving vault. %w", err)
	}

	return nil
}

func getUserSecretsFilePath(vaultId string) (string, error) {
	configPath, err := GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed getting user vault file path '%s'. %w", configPath, err)
	}

	return filepath.Join(configPath, ".envVaults", vaultId+".json"), nil
}
