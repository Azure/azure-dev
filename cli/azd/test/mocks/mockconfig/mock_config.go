// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mockconfig

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

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

func (m *MockConfigManager) SaveWithContext(ctx context.Context, config config.Config, filePath string) error {
	return m.Save(config, filePath)
}

func (m *MockConfigManager) Load(filePath string) (config.Config, error) {
	return m.config, nil
}
