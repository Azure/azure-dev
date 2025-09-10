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
				`List Azure resources required for a specific technology stack.

Returns baseline resources (required for all stacks) plus stack-specific resources for the chosen technology approach.

Use this tool when:
- A technology stack has been selected for a project (containers, serverless, or logic apps)
- Need resource listing for the selected technology stack

The tool takes a stack parameter (containers, serverless, logic_apps) and returns the comprehensive " +
				"resource list with Azure resource type identifiers.`,
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
