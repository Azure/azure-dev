package io

import (
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/tools"
)

// IoToolsLoader loads IO-related tools
type IoToolsLoader struct {
	callbackHandler callbacks.Handler
}

func NewIoToolsLoader(callbackHandler callbacks.Handler) *IoToolsLoader {
	return &IoToolsLoader{
		callbackHandler: callbackHandler,
	}
}

func (l *IoToolsLoader) LoadTools() ([]tools.Tool, error) {
	return []tools.Tool{
		&CurrentDirectoryTool{CallbacksHandler: l.callbackHandler},
		&ChangeDirectoryTool{CallbacksHandler: l.callbackHandler},
		&DirectoryListTool{CallbacksHandler: l.callbackHandler},
		&CreateDirectoryTool{CallbacksHandler: l.callbackHandler},
		&DeleteDirectoryTool{CallbacksHandler: l.callbackHandler},
		&ReadFileTool{CallbacksHandler: l.callbackHandler},
		&WriteFileTool{CallbacksHandler: l.callbackHandler},
		&CopyFileTool{CallbacksHandler: l.callbackHandler},
		&MoveFileTool{CallbacksHandler: l.callbackHandler},
		&DeleteFileTool{CallbacksHandler: l.callbackHandler},
		&FileInfoTool{CallbacksHandler: l.callbackHandler},
		&FileSearchTool{CallbacksHandler: l.callbackHandler},
	}, nil
}
