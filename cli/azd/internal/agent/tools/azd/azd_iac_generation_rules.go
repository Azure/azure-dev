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
	return `
		Gets the infrastructure as code (IaC) rules and best practices and patterns to use when generating bicep files and modules for use within AZD.

		Input: "./azd-arch-plan.md"
	`
}

func (t *AzdIacGenerationRulesTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdIacRulesPrompt, nil
}
