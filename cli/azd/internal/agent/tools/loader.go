// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"github.com/tmc/langchaingo/tools"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/dev"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/io"
)

// ToolLoader provides an interface for loading tools from different categories
type ToolLoader interface {
	LoadTools() ([]tools.Tool, error)
}

// LocalToolsLoader manages loading tools from multiple local tool categories
type LocalToolsLoader struct {
	loaders []ToolLoader
}

// NewLocalToolsLoader creates a new instance with default tool loaders for dev and io categories
func NewLocalToolsLoader() *LocalToolsLoader {
	return &LocalToolsLoader{
		loaders: []ToolLoader{
			dev.NewDevToolsLoader(),
			io.NewIoToolsLoader(),
		},
	}
}

// LoadTools loads and returns all tools from all registered tool loaders.
// Returns an error if any individual loader fails to load its tools.
func (l *LocalToolsLoader) LoadTools() ([]tools.Tool, error) {
	var allTools []tools.Tool

	for _, loader := range l.loaders {
		categoryTools, err := loader.LoadTools()
		if err != nil {
			return nil, err
		}
		allTools = append(allTools, categoryTools...)
	}

	return allTools, nil
}
