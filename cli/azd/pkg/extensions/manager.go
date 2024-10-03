package extensions

import (
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

var (
	ErrNotFound = errors.New("extension not found")
)

type Manager struct {
	configManager config.UserConfigManager
	userConfig    config.Config
	extensions    map[string]*Extension
}

func NewManager(configManager config.UserConfigManager) *Manager {
	return &Manager{
		configManager: configManager,
	}
}

func (m *Manager) Initialize() (map[string]*Extension, error) {
	userConfig, err := m.configManager.Load()
	if err != nil {
		return nil, err
	}

	m.userConfig = userConfig

	var extensions map[string]*Extension
	ok, err := m.userConfig.GetSection("extensions", &extensions)
	if err != nil {
		return nil, fmt.Errorf("failed to get extensions section: %w", err)
	}

	if !ok {
		return nil, nil
	}

	m.extensions = extensions

	return extensions, nil
}

func (m *Manager) Get(name string) (*Extension, error) {
	if extension, has := m.extensions[name]; has {
		return extension, nil
	}

	return nil, fmt.Errorf("%s %w", name, ErrNotFound)
}
