package azd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/azd/prompts"
	"github.com/tmc/langchaingo/tools"
)

var _ tools.Tool = &AzdInfrastructureGenerationTool{}

type AzdInfrastructureGenerationTool struct {
}

func (t *AzdInfrastructureGenerationTool) Name() string {
	return "azd_infrastructure_generation"
}

func (t *AzdInfrastructureGenerationTool) Description() string {
	return `
		Generates Bicep infrastructure templates for Azure Developer CLI (AZD) projects.
		This specialized tool focuses on creating modular Bicep templates, parameter files,
		and implementing Azure security and operational best practices for infrastructure as code.

		Input: "./azd-arch-plan.md"
	`
}

func (t *AzdInfrastructureGenerationTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdInfrastructureGenerationPrompt, nil
}
