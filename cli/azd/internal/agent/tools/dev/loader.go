// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dev

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
)

// DevToolLoader loads development-related tools
type DevToolsLoader struct{}

// NewDevToolsLoader creates a new instance of DevToolsLoader
func NewDevToolsLoader() common.ToolLoader {
	return &DevToolsLoader{}
}

// LoadTools loads and returns all development-related tools
func (l *DevToolsLoader) LoadTools(ctx context.Context) ([]common.AnnotatedTool, error) {
	return []common.AnnotatedTool{
		&CommandExecutorTool{},
	}, nil
}
