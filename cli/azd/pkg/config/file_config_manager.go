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

	return azdConfig, nil
}

func (m *fileConfigManager) Save(c Config, filePath string) error {
	folderPath := filepath.Dir(filePath)
	if err := os.MkdirAll(folderPath, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("failed creating config directory: %w", err)
	}

	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("failed creating config directory: %w", err)
	}
	defer file.Close()

	err = m.manager.Save(c, file)
	if err != nil {
		return err
	}

	return nil
}
