// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdGenerateInfraModuleTool creates a new tool for generating Bicep infrastructure modules
func NewAzdGenerateInfraModuleTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_generate_infra_module",
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Generates a new Bicep infrastructure module for a specified Azure resource type. 
This tool takes resource type, API version, and optional requirements as input and returns 
a complete Bicep module using MCP sampling.`,
			),
			mcp.WithString("resourceType",
				mcp.Description("The Azure resource type for the module (e.g., Microsoft.Storage/storageAccounts, Microsoft.Web/sites)"),
				mcp.Required(),
			),
			mcp.WithString("apiVersion",
				mcp.Description("The API version to use for the resource. Defaults to 'latest' if not specified"),
			),
			mcp.WithString("requirements",
				mcp.Description("Optional specific requirements or configurations for the module"),
			),
		),
		Handler: handleAzdGenerateInfraModule,
	}
}

func handleAzdGenerateInfraModule(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Validate required parameters
	resourceType, err := request.RequireString("resourceType")
	if err != nil {
		return createErrorResult("resourceType", "Resource type is required"), nil
	}

	if strings.TrimSpace(resourceType) == "" {
		return createErrorResult("resourceType", "Resource type cannot be empty"), nil
	}

	// Get optional parameters
	apiVersion := request.GetString("apiVersion", "latest")
	requirements := request.GetString("requirements", "")

	// Construct the prompt for Bicep module generation
	prompt := buildBicepModulePrompt(resourceType, apiVersion, requirements)

	// Make MCP sampling request
	serverFromCtx := server.ServerFromContext(ctx)
	samplingRequest := mcp.CreateMessageRequest{
		CreateMessageParams: mcp.CreateMessageParams{
			Messages: []mcp.SamplingMessage{
				{
					Role: mcp.RoleUser,
					Content: mcp.TextContent{
						Type: "text",
						Text: prompt,
					},
				},
			},
			SystemPrompt: `You are an expert Bicep Code Generator that can generate production-ready Bicep modules that follow Azure Well-Architected Framework principles.`,
			Temperature:  0.3, // Lower temperature for more consistent code generation
		},
	}

	samplingResult, err := serverFromCtx.RequestSampling(ctx, samplingRequest)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Error during Bicep module generation", err), nil
	}

	// Extract the generated Bicep code
	if textContent, ok := samplingResult.Content.(mcp.TextContent); ok {
		bicepCode := textContent.Text

		// Format the response - construct without backticks in the format string
		response := "# Generated Bicep Module\n\n"
		response += "## Resource Information\n"
		response += fmt.Sprintf("- **Resource Type**: %s\n", resourceType)
		response += fmt.Sprintf("- **API Version**: %s\n", apiVersion)
		response += fmt.Sprintf("- **Requirements**: %s\n\n", getRequirementsDisplay(requirements))
		response += "## Generated Bicep Code\n\n"
		response += "```bicep\n"
		response += bicepCode
		response += "\n```"

		return mcp.NewToolResultText(response), nil
	}

	return mcp.NewToolResultError("Failed to extract Bicep code from sampling response"), nil
}

func buildBicepModulePrompt(resourceType, apiVersion, requirements string) string {
	prompt := fmt.Sprintf(`Generate a complete Bicep module for the Azure resource type: %s

Requirements:
- Use API version: %s
- Include proper parameter definitions with descriptions and validation
- Add meaningful variable declarations where appropriate
- Include comprehensive outputs for important resource properties
- Follow Azure naming conventions and best practices
- Add resource tags for governance
- Include proper documentation comments`, resourceType, apiVersion)

	if requirements != "" {
		prompt += fmt.Sprintf(`
- Additional specific requirements: %s`, requirements)
	}

	prompt += `

The module should be production-ready and include:
1. Parameter section with proper types, descriptions, and validation
2. Variable section for computed values
3. Resource definition with all necessary properties
4. Output section exposing key resource properties
5. Inline comments explaining key configurations

Please provide only the Bicep code without additional explanations or markdown formatting.`

	return prompt
}

func getRequirementsDisplay(requirements string) string {
	if requirements == "" {
		return "None specified"
	}
	return requirements
}

func createErrorResult(parameterName, message string) *mcp.CallToolResult {
	fullMessage := fmt.Sprintf("Parameter '%s': %s", parameterName, message)
	errorData := ErrorResponse{
		Error:   true,
		Message: fullMessage,
	}

	errorJSON, _ := json.Marshal(errorData)
	return mcp.NewToolResultText(string(errorJSON))
}
