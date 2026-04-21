// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// validVaultIDPattern matches vault IDs containing only alphanumeric characters, hyphens, and underscores.
var validVaultIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// FileConfigManager provides the ability to load, parse and save azd configuration files
type FileConfigManager interface {
	// Saves the azd configuration to the specified file path
	// Path is automatically created if it does not exist
	Save(config Config, filePath string) error

	// Loads azd configuration from the specified file path
	Load(filePath string) (Config, error)
}

// NewFileConfigManager creates a new FileConfigManager instance
func NewFileConfigManager(configManager Manager) FileConfigManager {
	return &fileConfigManager{
		manager: configManager,
	}
}

type fileConfigManager struct {
	manager Manager
}

func (m *fileConfigManager) Load(filePath string) (Config, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed opening azd configuration file: %w", err)
	}

	defer file.Close()

	azdConfig, err := m.manager.Load(file)
	if err != nil {
		return nil, err
	}

	// If the configuration contains a vault, then also load the vault configuration
	vaultId, ok := azdConfig.GetString(vaultKeyName)
	if ok {
		vaultPath, err := resolveVaultPath(vaultId)
		if err != nil {
			return nil, err
		}

		vaultConfig, err := m.Load(vaultPath)
		if err != nil {
			return nil, fmt.Errorf("failed loading vault configuration from '%s': %w", vaultPath, err)
		}

		baseConfig, ok := azdConfig.(*config)
		if !ok {
			return nil, fmt.Errorf("failed casting azd configuration to config")
		}

		baseConfig.vaultId = vaultId
		baseConfig.vault = vaultConfig
	}

	return azdConfig, nil
}

func (m *fileConfigManager) Save(c Config, filePath string) error {
	folderPath := filepath.Dir(filePath)
	if err := os.MkdirAll(folderPath, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("failed creating config directory: %w", err)
	}

	// Atomic write: write to a temp file in the same directory then rename.
	// This prevents corruption if the process is interrupted mid-write.
	tmpFile, err := os.CreateTemp(folderPath, ".azd-config-*.tmp")
	if err != nil {
		return fmt.Errorf("failed creating temp config file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// On any failure path, clean up the temp file.
	success := false
	defer func() {
		if !success {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if err := m.manager.Save(c, tmpFile); err != nil {
		return err
	}

	// Flush and close before rename to ensure all data is on disk.
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed syncing temp config file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed closing temp config file: %w", err)
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		return fmt.Errorf("failed renaming temp config file: %w", err)
	}
	success = true

	baseConfig, ok := c.(*config)
	if !ok {
		return fmt.Errorf("failed casting azd configuration to config")
	}

	// If the configuration contains a vault, then also save the vault configuration
	// Vault configuration always gets saved in a separate file in the users HOME directory.
	if baseConfig.vaultId != "" {
		vaultPath, err := resolveVaultPath(baseConfig.vaultId)
		if err != nil {
			return err
		}

		if err = os.MkdirAll(filepath.Dir(vaultPath), osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("failed creating vaults directory: %w", err)
		}

		return m.Save(baseConfig.vault, vaultPath)
	}

	return nil
}

// resolveVaultPath validates a vault ID and returns the full path to the vault JSON file.
// It enforces an allowlist of safe characters and verifies the resolved path stays within the vaults directory.
func resolveVaultPath(vaultId string) (string, error) {
	if !validVaultIDPattern.MatchString(vaultId) {
		return "", fmt.Errorf(
			"invalid vault ID %q: must contain only alphanumeric characters, hyphens, and underscores",
			vaultId,
		)
	}

	configPath, err := GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed getting user config directory: %w", err)
	}

	vaultsDir := filepath.Join(configPath, "vaults")
	vaultPath := filepath.Join(vaultsDir, fmt.Sprintf("%s.json", vaultId))

	// Defense-in-depth: also verify the resolved path stays within the vaults directory
	if !osutil.IsPathContained(vaultsDir, vaultPath) {
		return "", fmt.Errorf("invalid vault ID %q: resolved path is outside the vaults directory", vaultId)
	}

	return vaultPath, nil
}
