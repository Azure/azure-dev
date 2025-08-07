package azd

import (
	"context"
	_ "embed"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/azd/prompts"
	"github.com/tmc/langchaingo/tools"
)

// AzdProjectValidationTool validates an AZD project by running comprehensive checks on all components
// including azure.yaml schema validation, Bicep template validation, environment setup, packaging,
// and deployment preview.
type AzdProjectValidationTool struct{}

// Name returns the name of the tool.
func (t *AzdProjectValidationTool) Name() string {
	return "azd_project_validation"
}

// Description returns the description of the tool.
func (t *AzdProjectValidationTool) Description() string {
	return `Returns instructions for validating AZD project by running comprehensive checks on azure.yaml schema, Bicep templates, environment setup, packaging, and deployment preview. The LLM agent should execute these instructions using available tools.

Use this tool when:
- All AZD configuration files have been generated
- Ready to validate complete project before deployment
- Need to ensure azure.yaml, Bicep templates, and environment are properly configured
- Final validation step before running azd up

Input: "./azd-arch-plan.md"`
}

// Call executes the tool with the given arguments.
func (t *AzdProjectValidationTool) Call(ctx context.Context, args string) (string, error) {
	return prompts.AzdProjectValidationPrompt, nil
}

// Ensure AzdProjectValidationTool implements the Tool interface.
var _ tools.Tool = (*AzdProjectValidationTool)(nil)
