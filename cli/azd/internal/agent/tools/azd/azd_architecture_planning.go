// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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
	return `Returns instructions for selecting appropriate Azure services for discovered application components and 
designing infrastructure architecture. 

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Discovery analysis has been completed and azd-arch-plan.md exists
- Application components have been identified and classified
- Need to map components to Azure hosting services
- Ready to plan containerization and database strategies

Input: "./azd-arch-plan.md"`
}

func (t *AzdArchitecturePlanningTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdArchitecturePlanningPrompt, nil
}
