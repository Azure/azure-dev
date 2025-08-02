package azd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/azd/prompts"
	"github.com/tmc/langchaingo/tools"
)

var _ tools.Tool = &AzdYamlSchemaTool{}

type AzdYamlSchemaTool struct {
}

func (t *AzdYamlSchemaTool) Name() string {
	return "azd_yaml_schema"
}

func (t *AzdYamlSchemaTool) Description() string {
	return `
		Gets the Azure YAML JSON schema file specification and structure for azure.yaml configuration files used in AZD.

		Input: <Relative path to azure.yaml>
	`
}

func (t *AzdYamlSchemaTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdYamlSchemaPrompt, nil
}
