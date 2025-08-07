package azd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/azd/prompts"
	"github.com/tmc/langchaingo/tools"
)

var _ tools.Tool = &AzdIacGenerationRulesTool{}

type AzdIacGenerationRulesTool struct {
}

func (t *AzdIacGenerationRulesTool) Name() string {
	return "azd_iac_generation_rules"
}

func (t *AzdIacGenerationRulesTool) Description() string {
	return `Returns comprehensive rules and guidelines for generating Bicep Infrastructure as Code files and modules for AZD projects. The LLM agent should reference these rules when generating infrastructure code.

Use this tool when:
- Generating any Bicep infrastructure templates for AZD projects
- Need compliance rules and naming conventions for Azure resources
- Creating modular, reusable Bicep files
- Ensuring security and operational best practices

Input: "./azd-arch-plan.md"`
}

func (t *AzdIacGenerationRulesTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdIacRulesPrompt, nil
}
