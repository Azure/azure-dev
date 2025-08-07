// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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
	return `Returns instructions for generating the azure.yaml configuration file with proper service hosting, 
build, and deployment settings for AZD projects. 

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Architecture planning has been completed and Azure services selected
- Need to create or update azure.yaml configuration file
- Services have been mapped to Azure hosting platforms
- Ready to define build and deployment configurations

Input: "./azd-arch-plan.md"`
}

func (t *AzdAzureYamlGenerationTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdAzureYamlGenerationPrompt, nil
}
