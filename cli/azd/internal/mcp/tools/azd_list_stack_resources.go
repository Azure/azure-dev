// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

//go:embed prompts/azd_list_stack_resources.md
var azdListStackResourcesPrompt string

//go:embed resources/baseline_resources.md
var baselineResourcesDoc string

//go:embed resources/containers_stack_resources.md
var containersStackResourcesDoc string

//go:embed resources/serverless_stack_resources.md
var serverlessStackResourcesDoc string

//go:embed resources/logic_apps_stack_resources.md
var logicAppsStackResourcesDoc string

func NewAzdListStackResourcesTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_list_stack_resources",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Provides the definitive list of Azure resources required for each technology stack (baseline and stack-specific) to guide architecture planning and infrastructure generation.

This tool returns detailed resource definitions that the LLM agent should use for architecture planning and infrastructure generation.

Use this tool when:
- Need to understand what Azure resources are available for each technology stack
- Planning infrastructure architecture and need resource type identifiers
- Ready to map application components to appropriate Azure services
- Generating infrastructure templates and need resource specifications`,
			),
			mcp.WithString("stack",
				mcp.Description("The technology stack to list resources for"),
				mcp.Enum("containers", "serverless", "logic_apps"),
				mcp.Required(),
			),
		),
		Handler: handleAzdListStackResources,
	}
}

func handleAzdListStackResources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	stack, err := request.RequireString("stack")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %v", err)), nil
	}

	// Validate stack parameter
	validStacks := map[string]bool{
		"containers": true,
		"serverless": true,
		"logic_apps": true,
	}

	if !validStacks[stack] {
		return mcp.NewToolResultError(
			fmt.Sprintf("Invalid stack '%s'. Must be one of: containers, serverless, logic_apps", stack),
		), nil
	}

	// Build response with baseline resources and stack-specific resources
	var stackSpecificDoc string
	var stackDisplayName string

	switch stack {
	case "containers":
		stackSpecificDoc = containersStackResourcesDoc
		stackDisplayName = "Containers"
	case "serverless":
		stackSpecificDoc = serverlessStackResourcesDoc
		stackDisplayName = "Serverless"
	case "logic_apps":
		stackSpecificDoc = logicAppsStackResourcesDoc
		stackDisplayName = "Logic Apps"
	}

	response := fmt.Sprintf(`%s

# Azure Resources for %s Stack

## Baseline Resources (All Stacks)

%s

## %s Stack-Specific Resources

%s`,
		azdListStackResourcesPrompt,
		stackDisplayName,
		baselineResourcesDoc,
		stackDisplayName,
		stackSpecificDoc,
	)

	return mcp.NewToolResultText(response), nil
}
