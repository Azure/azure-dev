// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"github.com/tmc/langchaingo/tools"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/azd"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/dev"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/io"
)

// ToolLoader provides an interface for loading tools from different categories
type ToolLoader interface {
	LoadTools() ([]tools.Tool, error)
}

type LocalToolsLoader struct {
	loaders []ToolLoader
}

func NewLocalToolsLoader() *LocalToolsLoader {
	return &LocalToolsLoader{
		loaders: []ToolLoader{
			azd.NewAzdToolsLoader(),
			dev.NewDevToolsLoader(),
			io.NewIoToolsLoader(),
		},
	}
}

// LoadLocalTools loads all tools from all categories with the provided callback handler
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
