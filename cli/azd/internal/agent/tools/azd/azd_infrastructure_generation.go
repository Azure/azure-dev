// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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
	return `Returns instructions for generating modular Bicep infrastructure templates following Azure security and 
operational best practices for AZD projects. 

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Architecture planning completed with Azure services selected
- Need to create Bicep infrastructure templates
- Ready to implement infrastructure as code for deployment

Input: "./azd-arch-plan.md"`
}

func (t *AzdInfrastructureGenerationTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdInfrastructureGenerationPrompt, nil
}
