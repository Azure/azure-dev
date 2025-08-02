package azd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/azd/prompts"
	"github.com/tmc/langchaingo/tools"
)

var _ tools.Tool = &AzdPlanInitTool{}

type AzdPlanInitTool struct {
}

func (t *AzdPlanInitTool) Name() string {
	return "azd_plan_init"
}

func (t *AzdPlanInitTool) Description() string {
	return `
		Gets the required workflow steps and best practices and patterns for initializing or migrating an application to use AZD.

		Input: "./azd-arch-plan.md"
	`
}

func (t *AzdPlanInitTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdPlanInitPrompt, nil
}
