package environment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// Config contains environment-related configuration.
type Config = config.Config

type ConfigManager struct {
	// Path to configuration file on filesystem
	path    string
	persist config.Manager
}

func NewConfigManager(
	azdCtx azdcontext.AzdContext,
	envName string,
	configMgr config.Manager) *ConfigManager {
	envRoot := azdCtx.EnvironmentRoot(envName)
	return &ConfigManager{
		path:    filepath.Join(envRoot, azdcontext.ConfigFileName),
		persist: configMgr,
	}
}

func NewConfigManagerFromRoot(
	root string,
	configMgr config.Manager) *ConfigManager {
	return &ConfigManager{
		path:    filepath.Join(root, azdcontext.ConfigFileName),
		persist: configMgr,
	}
}

// Load retrieves the stored configuration. If not present, an empty configuration is returned.
func (m *ConfigManager) Load() (Config, error) {
	cfg, err := m.persist.Load(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return config.NewConfig(nil), nil
	} else if err != nil {
		return nil, fmt.Errorf("loading environment config: %w", err)
	}
	return cfg, nil
}

func (m *ConfigManager) Save(c Config) error {
	err := os.MkdirAll(filepath.Dir(m.path), osutil.PermissionDirectory)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	err = m.persist.Save(c, m.path)
	if err != nil {
		return fmt.Errorf("failed saving environment config: %w", err)
	}

	return nil
}
