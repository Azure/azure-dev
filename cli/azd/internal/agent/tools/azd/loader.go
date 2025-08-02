package azd

import (
	"github.com/tmc/langchaingo/tools"
)

// AzdToolsLoader loads AZD-related tools
type AzdToolsLoader struct{}

func NewAzdToolsLoader() *AzdToolsLoader {
	return &AzdToolsLoader{}
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
