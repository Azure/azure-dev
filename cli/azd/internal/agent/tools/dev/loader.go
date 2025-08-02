package dev

import (
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/tools"
)

// DevToolLoader loads development-related tools
type DevToolsLoader struct {
	callbacksHandler callbacks.Handler
}

func NewDevToolsLoader(callbacksHandler callbacks.Handler) *DevToolsLoader {
	return &DevToolsLoader{
		callbacksHandler: callbacksHandler,
	}
}

func (l *DevToolsLoader) LoadTools() ([]tools.Tool, error) {
	return []tools.Tool{
		&CommandExecutorTool{CallbacksHandler: l.callbacksHandler},
	}, nil
}
