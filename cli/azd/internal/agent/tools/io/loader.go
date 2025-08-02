package io

import (
	"github.com/tmc/langchaingo/tools"
)

// IoToolsLoader loads IO-related tools
type IoToolsLoader struct{}

func NewIoToolsLoader() *IoToolsLoader {
	return &IoToolsLoader{}
}

func (l *IoToolsLoader) LoadTools() ([]tools.Tool, error) {
	return []tools.Tool{
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
