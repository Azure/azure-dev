// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/internal/agent/security"
	"github.com/azure/azure-dev/internal/agent/tools/common"
	"github.com/azure/azure-dev/internal/agent/tools/dev"
	"github.com/azure/azure-dev/internal/agent/tools/io"
)

// LocalToolsLoader manages loading tools from multiple local tool categories
type LocalToolsLoader struct {
	loaders []common.ToolLoader
}

// NewLocalToolsLoader creates a new instance with default tool loaders for dev and io categories
func NewLocalToolsLoader(securityManager *security.Manager) common.ToolLoader {
	return &LocalToolsLoader{
		loaders: []common.ToolLoader{
			dev.NewDevToolsLoader(),
			io.NewIoToolsLoader(securityManager),
		},
	}
}

// LoadTools loads and returns all tools from all registered tool loaders.
// Returns an error if any individual loader fails to load its tools.
func (l *LocalToolsLoader) LoadTools(ctx context.Context) ([]common.AnnotatedTool, error) {
	var allTools []common.AnnotatedTool

	for _, loader := range l.loaders {
		categoryTools, err := loader.LoadTools(ctx)
		if err != nil {
			return nil, err
		}
		allTools = append(allTools, categoryTools...)
	}

	return allTools, nil
}
