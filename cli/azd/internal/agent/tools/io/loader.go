// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
)

// IoToolsLoader loads IO-related tools
type IoToolsLoader struct{}

// NewIoToolsLoader creates a new instance of IoToolsLoader
func NewIoToolsLoader() common.ToolLoader {
	return &IoToolsLoader{}
}

// LoadTools loads and returns all IO-related tools
func (l *IoToolsLoader) LoadTools() ([]common.AnnotatedTool, error) {
	return []common.AnnotatedTool{
		&CurrentDirectoryTool{},
		&ChangeDirectoryTool{},
		&DirectoryListTool{},
		&CreateDirectoryTool{},
		&DeleteDirectoryTool{},
		&ReadFileTool{},
		&WriteFileTool{},
		&CopyFileTool{},
		&MoveFileTool{},
		&DeleteFileTool{},
		&FileInfoTool{},
		&FileSearchTool{},
	}, nil
}
