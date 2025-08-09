// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dev

import "github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"

// DevToolLoader loads development-related tools
type DevToolsLoader struct{}

func NewDevToolsLoader() *DevToolsLoader {
	return &DevToolsLoader{}
}

func (l *DevToolsLoader) LoadTools() ([]common.Tool, error) {
	return []common.Tool{
		&CommandExecutorTool{},
	}, nil
}
