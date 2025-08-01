package azd

import (
	"context"

	"azd.ai.start/internal/tools/azd/prompts"
	"github.com/tmc/langchaingo/tools"
)

var _ tools.Tool = &AzdDiscoveryAnalysisTool{}

type AzdDiscoveryAnalysisTool struct {
}

func (t *AzdDiscoveryAnalysisTool) Name() string {
	return "azd_discovery_analysis"
}

func (t *AzdDiscoveryAnalysisTool) Description() string {
	return `
		Performs comprehensive discovery and analysis of applications to prepare them for Azure Developer CLI (AZD) initialization. 
		This is Phase 1 of the AZD migration process that analyzes codebase, identifies components and dependencies, 
		and creates a foundation for architecture planning.

		Input: "./azd-arch-plan.md"
	`
}

func (t *AzdDiscoveryAnalysisTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdDiscoveryAnalysisPrompt, nil
}
