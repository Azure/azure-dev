package azd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/azd/prompts"
	"github.com/tmc/langchaingo/tools"
)

var _ tools.Tool = &AzdArchitecturePlanningTool{}

type AzdArchitecturePlanningTool struct {
}

func (t *AzdArchitecturePlanningTool) Name() string {
	return "azd_architecture_planning"
}

func (t *AzdArchitecturePlanningTool) Description() string {
	return `
		Performs Azure service selection and architecture planning for applications preparing for Azure Developer CLI (AZD) initialization.
		This is Phase 2 of the AZD migration process that maps components to Azure services, plans hosting strategies,
		and designs infrastructure architecture based on discovery results.

		Input: "./azd-arch-plan.md"
	`
}

func (t *AzdArchitecturePlanningTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdArchitecturePlanningPrompt, nil
}
