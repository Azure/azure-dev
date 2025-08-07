// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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
	return `Returns instructions for orchestrating complete AZD application initialization using structured phases 
with specialized tools. 

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Starting new AZD project initialization or migration
- Need structured approach to transform application into AZD-compatible project
- Want to ensure proper sequencing of discovery, planning, and file generation
- Require complete project orchestration guidance

Input: "./azd-arch-plan.md"`
}

func (t *AzdPlanInitTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdPlanInitPrompt, nil
}
