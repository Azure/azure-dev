package azd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/azd/prompts"
	"github.com/tmc/langchaingo/tools"
)

var _ tools.Tool = &AzdAzureYamlGenerationTool{}

type AzdAzureYamlGenerationTool struct {
}

func (t *AzdAzureYamlGenerationTool) Name() string {
	return "azd_azure_yaml_generation"
}

func (t *AzdAzureYamlGenerationTool) Description() string {
	return `
		Generates the azure.yaml configuration file for Azure Developer CLI (AZD) projects. 
		This specialized tool focuses on creating service definitions, hosting configurations,
		and deployment instructions. Can be used independently for service configuration updates.

		Input: "./azd-arch-plan.md"
	`
}

func (t *AzdAzureYamlGenerationTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdAzureYamlGenerationPrompt, nil
}
