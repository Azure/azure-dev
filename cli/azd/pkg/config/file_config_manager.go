package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

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
		configPath, err := GetUserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("failed getting user config directory: %w", err)
		}

		vaultPath := filepath.Join(configPath, "vaults", fmt.Sprintf("%s.json", vaultId))
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

	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("failed creating config directory: %w", err)
	}
	defer file.Close()

	err = m.manager.Save(c, file)
	if err != nil {
		return err
	}

	baseConfig, ok := c.(*config)
	if !ok {
		return fmt.Errorf("failed casting azd configuration to config")
	}

	// If the configuration contains a vault, then also save the vault configuration
	// Vault configuration always gets saved in a separate file in the users HOME directory.
	if baseConfig.vaultId != "" {
		configPath, err := GetUserConfigDir()
		if err != nil {
			return fmt.Errorf("failed getting user config directory: %w", err)
		}

		vaultPath := filepath.Join(configPath, "vaults", fmt.Sprintf("%s.json", baseConfig.vaultId))
		if err = os.MkdirAll(filepath.Dir(vaultPath), osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("failed creating vaults directory: %w", err)
		}

		return m.Save(baseConfig.vault, vaultPath)
	}

	return nil
}
