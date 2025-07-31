package azd

import (
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/tools"
)

// AzdToolsLoader loads AZD-related tools
type AzdToolsLoader struct {
	callbackHandler callbacks.Handler
}

func NewAzdToolsLoader(callbackHandler callbacks.Handler) *AzdToolsLoader {
	return &AzdToolsLoader{
		callbackHandler: callbackHandler,
	}
}

func (l *AzdToolsLoader) LoadTools() ([]tools.Tool, error) {
	return []tools.Tool{
		&AzdPlanInitTool{},
		&AzdIacGenerationRulesTool{},
		&AzdYamlSchemaTool{},
	}, nil
}
