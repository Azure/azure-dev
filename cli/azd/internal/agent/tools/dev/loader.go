package dev

import (
	"github.com/tmc/langchaingo/tools"
)

// DevToolLoader loads development-related tools
type DevToolsLoader struct{}

func NewDevToolsLoader() *DevToolsLoader {
	return &DevToolsLoader{}
}

func (l *DevToolsLoader) LoadTools() ([]tools.Tool, error) {
	return []tools.Tool{
		&CommandExecutorTool{},
	}, nil
}
