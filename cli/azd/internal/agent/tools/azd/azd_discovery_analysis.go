package azd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/azd/prompts"
	"github.com/tmc/langchaingo/tools"
)

var _ tools.Tool = &AzdDiscoveryAnalysisTool{}

type AzdDiscoveryAnalysisTool struct {
}

func (t *AzdDiscoveryAnalysisTool) Name() string {
	return "azd_discovery_analysis"
}

func (t *AzdDiscoveryAnalysisTool) Description() string {
	return `Returns instructions for performing comprehensive discovery and analysis of application components to prepare for Azure Developer CLI (AZD) initialization. The LLM agent should execute these instructions using available tools.

Use this tool when:
- Starting Phase 1 of AZD migration process
- Need to identify all application components and dependencies
- Codebase analysis required before architecture planning
- azd-arch-plan.md does not exist or needs updating

Input: "./azd-arch-plan.md"`
}

func (t *AzdDiscoveryAnalysisTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdDiscoveryAnalysisPrompt, nil
}
