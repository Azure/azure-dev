package azd

import (
	"context"
	_ "embed"

	"github.com/tmc/langchaingo/tools"
)

//go:embed prompts/azd_project_validation.md
var azdProjectValidationPrompt string

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
	return `
		Validates an AZD project by running comprehensive checks on all components including azure.yaml schema validation, Bicep template validation, environment setup, packaging, and deployment preview.

		Input: "./azd-arch-plan.md"`
}

// Call executes the tool with the given arguments.
func (t *AzdProjectValidationTool) Call(ctx context.Context, args string) (string, error) {
	return azdProjectValidationPrompt, nil
}

// Ensure AzdProjectValidationTool implements the Tool interface.
var _ tools.Tool = (*AzdProjectValidationTool)(nil)
