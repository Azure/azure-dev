package mockconfig

import "github.com/azure/azure-dev/cli/azd/pkg/config"

type MockConfigManager struct {
	config config.Config
}

func NewMockConfigManager() *MockConfigManager {
	return &MockConfigManager{
		config: config.NewEmptyConfig(),
	}
}

func (m *MockConfigManager) WithConfig(config config.Config) *MockConfigManager {
	m.config = config
	return m
}

func (m *MockConfigManager) Save(config config.Config, filePath string) error {
	return nil
}

func (m *MockConfigManager) Load(filePath string) (config.Config, error) {
	return m.config, nil
}
