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
		// Original orchestrating tool
		&AzdPlanInitTool{},

		// Core workflow tools (use in sequence)
		&AzdDiscoveryAnalysisTool{},
		&AzdArchitecturePlanningTool{},

		// Focused file generation tools (use as needed)
		&AzdAzureYamlGenerationTool{},
		&AzdInfrastructureGenerationTool{},
		&AzdDockerGenerationTool{},

		// Validation tool (final step)
		&AzdProjectValidationTool{},

		// Supporting tools
		&AzdIacGenerationRulesTool{},
		&AzdYamlSchemaTool{},
	}, nil
}
