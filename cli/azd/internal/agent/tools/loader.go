package tools

import (
	"github.com/tmc/langchaingo/callbacks"
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
	loaders         []ToolLoader
	callbackHandler callbacks.Handler
}

func NewLocalToolsLoader(callbackHandler callbacks.Handler) *LocalToolsLoader {
	return &LocalToolsLoader{
		loaders: []ToolLoader{
			azd.NewAzdToolsLoader(callbackHandler),
			dev.NewDevToolsLoader(callbackHandler),
			io.NewIoToolsLoader(callbackHandler),
		},
		callbackHandler: callbackHandler,
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
