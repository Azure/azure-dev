// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

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

// ContextualFileConfigManager is an optional FileConfigManager capability for
// context-aware persistence.
type ContextualFileConfigManager interface {
	FileConfigManager
	SaveWithContext(ctx context.Context, config Config, filePath string) error
}

func saveFileConfig(ctx context.Context, manager FileConfigManager, config Config, filePath string) error {
	if contextualManager, ok := manager.(ContextualFileConfigManager); ok {
		return contextualManager.SaveWithContext(ctx, config, filePath)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return manager.Save(config, filePath)
}

// NewFileConfigManager creates a new FileConfigManager instance
func NewFileConfigManager(configManager Manager) FileConfigManager {
	return &fileConfigManager{
		manager: configManager,
	}
}

type fileConfigManager struct {
	mu      sync.Mutex
	manager Manager
}

func (m *fileConfigManager) Load(filePath string) (Config, error) {
	data, err := osutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading azd configuration file: %w", err)
	}

	azdConfig, err := m.manager.Load(bytes.NewReader(data))
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
	return m.SaveWithContext(context.Background(), c, filePath)
}

func (m *fileConfigManager) SaveWithContext(ctx context.Context, c Config, filePath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	baseConfig, ok := c.(*config)
	if !ok {
		return fmt.Errorf("failed casting azd configuration to config")
	}

	folderPath := filepath.Dir(filePath)
	if err := os.MkdirAll(folderPath, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("failed creating config directory: %w", err)
	}

	var rootData bytes.Buffer
	if err := m.manager.Save(c, &rootData); err != nil {
		return fmt.Errorf("serializing file config: %w", err)
	}

	// If the configuration contains a vault, then also save the vault configuration
	// Vault configuration always gets saved in a separate file in the users HOME directory.
	if baseConfig.vaultId != "" {
		vaultPath, err := resolveVaultPath(baseConfig.vaultId)
		if err != nil {
			return err
		}
		if baseConfig.vault == nil {
			return fmt.Errorf("vault configuration '%s' is not loaded", baseConfig.vaultId)
		}

		if err = os.MkdirAll(filepath.Dir(vaultPath), osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("failed creating vaults directory: %w", err)
		}

		var vaultData bytes.Buffer
		if err := m.manager.Save(baseConfig.vault, &vaultData); err != nil {
			return fmt.Errorf("serializing vault configuration: %w", err)
		}
		if err := writeUserConfigFileAtomic(ctx, vaultPath, vaultData.Bytes()); err != nil {
			return fmt.Errorf("saving vault configuration: %w", err)
		}
	}

	if err := writeUserConfigFileAtomic(ctx, filePath, rootData.Bytes()); err != nil {
		return fmt.Errorf("saving file config: %w", err)
	}

	return nil
}

func writeUserConfigFileAtomic(ctx context.Context, path string, data []byte) error {
	perm := osutil.PermissionFileOwnerOnly
	if _, err := os.Stat(path); err == nil {
		perm = 0
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stating config target file: %w", err)
	}
	return osutil.WriteFileAtomic(ctx, path, data, perm)
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
